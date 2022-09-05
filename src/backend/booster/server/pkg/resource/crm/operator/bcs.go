/*
 * Copyright (c) 2021 THL A29 Limited, a Tencent company. All rights reserved
 *
 * This source code file is licensed under the MIT License, you may obtain a copy of the License at
 *
 * http://opensource.org/licenses/MIT
 *
 */

package operator

import (
	"fmt"
	"math"
	"sync"
	"time"

	"github.com/Tencent/bk-ci/src/booster/common/blog"
	"github.com/Tencent/bk-ci/src/booster/common/metric/controllers"
	"github.com/Tencent/bk-ci/src/booster/server/config"
	"github.com/Tencent/bk-ci/src/booster/server/pkg/engine"
	selfMetric "github.com/Tencent/bk-ci/src/booster/server/pkg/metric"
	rsc "github.com/Tencent/bk-ci/src/booster/server/pkg/resource"
)

// Operator define a bcs handler to do all operations.
type Operator interface {
	GetResource(clusterID string) ([]*NodeInfo, error)
	GetServerStatus(clusterID, namespace, name string) (*ServiceInfo, error)
	LaunchServer(clusterID string, param BcsLaunchParam) error
	ScaleServer(clusterID string, namespace, name string, instance int) error
	ReleaseServer(clusterID, namespace, name string) error
}

// BcsLaunchParam describe the launch request param through bcs
// including mesos/k8s/devcloud-mac
type BcsLaunchParam struct {
	// application name
	Name string

	// application namespace
	Namespace string

	// attribute condition, matters the constraint strategy
	AttributeCondition map[string]string

	// env key-values which will be inserted into containers
	Env map[string]string

	// ports that implements with port_name:protocol
	// such as my_port_alpha:http, my_port_beta:tcp
	// port numbers are all generated by container scheduler with cnm HOST
	Ports map[string]string

	// volumes implements the hostPath volumes with name:settings
	Volumes map[string]BcsVolume

	// container images
	Image string

	// instance number to launch
	Instance int
}

// BcsVolume describe the volume mapping settings
type BcsVolume struct {
	ContainerDir string
	HostDir      string
}

const (
	AttributeKeyCity     = "City"
	AttributeKeyPlatform = "Platform"
)

//CheckQueueKey describe the function that get queue key from attributes
func (param *BcsLaunchParam) CheckQueueKey(instanceType config.InstanceType) bool {
	platform, city := getInstanceKey(param.AttributeCondition)
	if instanceType.Group == city && instanceType.Platform == platform {
		return true
	}
	return false
}

// InstanceFilterFunction describe the function that decide how many instance to launch/scale.
type InstanceFilterFunction func(availableInstance int) (int, error)

// NodeInfo 描述了从各个operator处获取的集群单个节点信息,
// 用于获取节点的标签, 资源使用情况等
type NodeInfo struct {
	IP         string
	Hostname   string
	DiskTotal  float64
	MemTotal   float64
	CPUTotal   float64
	DiskUsed   float64
	MemUsed    float64
	CPUUsed    float64
	Attributes map[string]string

	Disabled bool
}

func (ni *NodeInfo) figureAvailableInstanceFromFree(cpuPerInstance, memPerInstance, diskPerInstance float64) int {
	if cpuPerInstance == 0 || memPerInstance == 0 || diskPerInstance == 0 {
		return 0
	}

	instanceByCPU := (ni.CPUTotal - ni.CPUUsed) / cpuPerInstance
	instanceByMem := (ni.MemTotal - ni.MemUsed) / memPerInstance
	instanceByDisk := (ni.DiskTotal - ni.DiskUsed) / diskPerInstance

	return int(math.Min(math.Min(instanceByCPU, instanceByMem), instanceByDisk))
}

func (ni *NodeInfo) valid() bool {
	return ni.CPUTotal >= 0 && ni.MemTotal >= 0 && ni.DiskTotal >= 0
}

// NewNodeInfoPool get a new node info pool
func NewNodeInfoPool(cpu, mem, disk float64, istTypes []config.InstanceType) *NodeInfoPool {
	nip := NodeInfoPool{
		cpuPerInstance:  cpu,
		memPerInstance:  mem,
		diskPerInstance: disk,
		nodeBlockMap:    make(map[string]*NodeInfoBlock, 1000),
	}
	for _, istItem := range istTypes {
		condition := map[string]string{
			AttributeKeyCity:     istItem.Group,
			AttributeKeyPlatform: istItem.Platform,
		}
		key := getBlockKey(condition)
		nip.nodeBlockMap[key] = &NodeInfoBlock{
			CPUPerInstance: istItem.CPUPerInstance,
			MemPerInstance: istItem.MemPerInstance,
		}
	}
	return &nip
}

// NodeInfoPool 描述了一个节点集合的资源情况, 一般用来管理整个集群的资源情况
type NodeInfoPool struct {
	sync.Mutex

	cpuPerInstance  float64
	memPerInstance  float64
	diskPerInstance float64
	lastUpdateTime  time.Time

	nodeBlockMap map[string]*NodeInfoBlock
}

// RecoverNoReadyBlock 对给定区域key的资源, 加上noReady个未就绪标记
// 一般用于在系统恢复时, 从数据库同步之前未就绪的数据信息
func (nip *NodeInfoPool) RecoverNoReadyBlock(key string, noReady int) {
	if _, ok := nip.nodeBlockMap[key]; !ok {
		nip.nodeBlockMap[key] = &NodeInfoBlock{}
	}

	nip.nodeBlockMap[key].noReadyInstance += noReady
}

// GetStats get status message
func (nip *NodeInfoPool) GetStats() string {
	nip.Lock()
	defer nip.Unlock()

	message := ""
	for city, block := range nip.nodeBlockMap {
		message += fmt.Sprintf(
			"\nkkkk City: %s[cpuPerInstance: %.2f, memPerInstance:%.2f], available-instance: %d, report-instance: %d, noready-instance: %d "+
				"CPU-Left: %.2f/%.2f, MEM-Left: %.2f/%.2f",
			city,
			block.CPUPerInstance,
			block.MemPerInstance,
			block.AvailableInstance-block.noReadyInstance,
			block.AvailableInstance, block.noReadyInstance,
			block.CPUTotal-block.CPUUsed,
			block.CPUTotal,
			block.MemTotal-block.MemUsed,
			block.MemTotal,
		)
	}
	return message
}

// GetDetail get detail data
func (nip *NodeInfoPool) GetDetail() []*rsc.RscDetails {
	nip.Lock()
	defer nip.Unlock()

	r := make([]*rsc.RscDetails, 0, len(nip.nodeBlockMap))
	for city, block := range nip.nodeBlockMap {
		r = append(r, &rsc.RscDetails{
			Labels:            city,
			CPUTotal:          block.CPUTotal,
			CPUUsed:           block.CPUUsed,
			MemTotal:          block.MemTotal,
			MemUsed:           block.MemUsed,
			CPUPerInstance:    nip.cpuPerInstance,
			AvailableInstance: block.AvailableInstance - block.noReadyInstance,
			ReportInstance:    block.AvailableInstance,
			NotReadyInstance:  block.noReadyInstance,
		})
	}

	return r
}

// GetLastUpdateTime 获取上次资源数据更新的时间
func (nip *NodeInfoPool) GetLastUpdateTime() time.Time {
	nip.Lock()
	defer nip.Unlock()

	return nip.lastUpdateTime
}

func (nip *NodeInfoPool) getNodeInstance(key string) (float64, float64) {
	cpuPerInstance := nip.cpuPerInstance
	memPerInstance := nip.memPerInstance
	if _, ok := nip.nodeBlockMap[key]; ok {
		if nip.nodeBlockMap[key].CPUPerInstance > 0.0 {
			cpuPerInstance = nip.nodeBlockMap[key].CPUPerInstance
		}
		if nip.nodeBlockMap[key].MemPerInstance > 0.0 {
			memPerInstance = nip.nodeBlockMap[key].MemPerInstance
		}
	}
	return cpuPerInstance, memPerInstance
}

// UpdateResources 更新资源数据, 给定从operators获取的节点信息列表, 将其信息与当前的资源信息进行整合同步
// - 已经消失的节点: 剔除
// - 新出现的节点: 增加
// - 更新的节点: 更新资源信息
func (nip *NodeInfoPool) UpdateResources(nodeInfoList []*NodeInfo) {
	nip.Lock()
	defer nip.Unlock()

	newBlockMap := make(map[string]*NodeInfoBlock, 1000)
	for _, NodeInfo := range nodeInfoList {
		go recordResource(NodeInfo)

		if NodeInfo.Disabled {
			continue
		}

		if !NodeInfo.valid() {
			blog.Warnf("crm: get node(%s) resources less than 0, cpu left: %.2f, memory left:%.2f, disk left:%.2f", NodeInfo.Hostname, NodeInfo.CPUUsed, NodeInfo.MemUsed, NodeInfo.DiskUsed)
			continue
		}

		key := getBlockKey(NodeInfo.Attributes)
		if _, ok := newBlockMap[key]; !ok {
			newBlockMap[key] = &NodeInfoBlock{}
		}

		newBlock := newBlockMap[key]
		newBlock.DiskTotal += NodeInfo.DiskTotal
		newBlock.MemTotal += NodeInfo.MemTotal
		newBlock.CPUTotal += NodeInfo.CPUTotal
		newBlock.DiskUsed += NodeInfo.DiskUsed
		newBlock.MemUsed += NodeInfo.MemUsed
		newBlock.CPUUsed += NodeInfo.CPUUsed
		//inherit the instance model if exist
		cpuPerInstance, memPerInstance := nip.getNodeInstance(key)
		newBlock.AvailableInstance += NodeInfo.figureAvailableInstanceFromFree(
			cpuPerInstance,
			memPerInstance,
			nip.diskPerInstance,
		)
		newBlock.CPUPerInstance = cpuPerInstance
		newBlock.MemPerInstance = memPerInstance
		// inherit the no-ready instance records
		if _, ok := nip.nodeBlockMap[key]; ok {
			newBlock.noReadyInstance = nip.nodeBlockMap[key].noReadyInstance
		}
	}

	for key, block := range newBlockMap {
		blog.Infof("kkkk: key:(%s), cpu used:(%v)", key, block.CPUUsed)
	}

	nip.nodeBlockMap = make(map[string]*NodeInfoBlock, 1000)
	for key, newBlock := range newBlockMap {
		if _, ok := nip.nodeBlockMap[key]; !ok {
			nip.nodeBlockMap[key] = &NodeInfoBlock{}
		}

		nodeBlock := nip.nodeBlockMap[key]
		nodeBlock.DiskTotal = newBlock.DiskTotal
		nodeBlock.MemTotal = newBlock.MemTotal
		nodeBlock.CPUTotal = newBlock.CPUTotal
		nodeBlock.DiskUsed = newBlock.DiskUsed
		nodeBlock.MemUsed = newBlock.MemUsed
		nodeBlock.CPUUsed = newBlock.CPUUsed
		nodeBlock.CPUPerInstance = newBlock.CPUPerInstance
		nodeBlock.MemPerInstance = newBlock.MemPerInstance
		nodeBlock.AvailableInstance = newBlock.AvailableInstance
		nodeBlock.noReadyInstance = newBlock.noReadyInstance
	}

	// record the last update time
	nip.lastUpdateTime = time.Now()
}

// GetFreeInstances 在资源池中尝试获取可用的instance, 给定需求条件condition和资源数量函数function
func (nip *NodeInfoPool) GetFreeInstances(
	condition map[string]string,
	function InstanceFilterFunction) (int, string, error) {

	nip.Lock()
	defer nip.Unlock()

	key := getBlockKey(condition)
	nodeBlock, ok := nip.nodeBlockMap[key]
	if !ok {
		return 0, key, engine.ErrorNoEnoughResources
	}

	need, err := function(nodeBlock.AvailableInstance - nodeBlock.noReadyInstance)
	if err != nil {
		return 0, key, err
	}

	if need+nodeBlock.noReadyInstance > nodeBlock.AvailableInstance {
		return 0, key, engine.ErrorNoEnoughResources
	}

	nodeBlock.noReadyInstance += need
	blog.V(5).Infof(
		"crm: get free instances consume %d instances from %s, current stats: report %d, no-ready: %d",
		need, key, nodeBlock.AvailableInstance, nodeBlock.noReadyInstance,
	)
	return need, key, nil
}

// ReleaseNoReadyInstance 消除给定区域的noReady计数, 表示这部分已经ready或已经释放
func (nip *NodeInfoPool) ReleaseNoReadyInstance(key string, instance int) {
	nip.Lock()
	defer nip.Unlock()

	nodeBlock, ok := nip.nodeBlockMap[key]
	if !ok {
		return
	}

	nodeBlock.noReadyInstance -= instance
	blog.V(5).Infof("crm: release %d no-ready instance from %s, current stats no-ready: %d",
		instance, key, nodeBlock.noReadyInstance)
}

// NodeInfoBlock 描述了一个特定区域的资源信息, 通常由多个区域组成一个完整的资源池NodeInfoPool
// 例如 shenzhen区, shanghai区, projectA区等等, 同一个NodeInfoBlock内的资源是统一处理的, 拥有共同的noReady计数
type NodeInfoBlock struct {
	DiskTotal      float64
	MemTotal       float64
	CPUTotal       float64
	DiskUsed       float64
	MemUsed        float64
	CPUUsed        float64
	CPUPerInstance float64
	MemPerInstance float64

	AvailableInstance int

	noReadyInstance int
}

// ServiceInfo 描述了已经消费了资源的服务信息
type ServiceInfo struct {
	Status             ServiceStatus
	Message            string
	RequestInstances   int
	CurrentInstances   int
	AvailableEndpoints []*Endpoint
}

type ServiceStatus int

const (
	// Container Service launched and not ready.
	ServiceStatusStaging ServiceStatus = iota

	// Container Service running successfully.
	ServiceStatusRunning

	// Container Service failed to be running.
	ServiceStatusFailed
)

// String get service status string.
func (ss ServiceStatus) String() string {
	return serviceStatusMap[ss]
}

var serviceStatusMap = map[ServiceStatus]string{
	ServiceStatusStaging: "staging",
	ServiceStatusRunning: "running",
	ServiceStatusFailed:  "failed",
}

// Endpoint 描述了单个消费了资源instance的服务的对外暴露的地址信息
type Endpoint struct {
	IP    string
	Ports map[string]int
}

func getInstanceKey(attributes map[string]string) (string, string) {
	city, ok := attributes[AttributeKeyCity]
	if !ok || city == "" {
		city = "unknown_city"
	}

	platform, _ := attributes[AttributeKeyPlatform]
	if platform == "" {
		platform = "default-platform"
	}
	return platform, city
}

func getBlockKey(attributes map[string]string) string {
	platform, city := getInstanceKey(attributes)
	return platform + "/" + city
}

func recordResource(node *NodeInfo) {
	metricLabels := controllers.ResourceStatusLabels{
		IP: node.IP, Zone: fmt.Sprintf("crm_%s", getBlockKey(node.Attributes)),
	}

	if node.Disabled {
		selfMetric.ResourceStatusController.UpdateCPUTotal(metricLabels, 0)
		selfMetric.ResourceStatusController.UpdateCPUUsed(metricLabels, 0)
		selfMetric.ResourceStatusController.UpdateMemTotal(metricLabels, 0)
		selfMetric.ResourceStatusController.UpdateMemUsed(metricLabels, 0)
		selfMetric.ResourceStatusController.UpdateDiskTotal(metricLabels, 0)
		selfMetric.ResourceStatusController.UpdateDiskUsed(metricLabels, 0)
		return
	}

	selfMetric.ResourceStatusController.UpdateCPUTotal(metricLabels, node.CPUTotal)
	selfMetric.ResourceStatusController.UpdateCPUUsed(metricLabels, node.CPUUsed)
	selfMetric.ResourceStatusController.UpdateMemTotal(metricLabels, node.MemTotal)
	selfMetric.ResourceStatusController.UpdateMemUsed(metricLabels, node.MemUsed)
	selfMetric.ResourceStatusController.UpdateDiskTotal(metricLabels, node.DiskTotal)
	selfMetric.ResourceStatusController.UpdateDiskUsed(metricLabels, node.DiskUsed)
}
