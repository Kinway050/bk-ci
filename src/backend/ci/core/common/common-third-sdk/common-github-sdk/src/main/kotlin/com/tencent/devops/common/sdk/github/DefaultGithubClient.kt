/*
 * Tencent is pleased to support the open source community by making BK-CI 蓝鲸持续集成平台 available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company.  All rights reserved.
 *
 * BK-CI 蓝鲸持续集成平台 is licensed under the MIT license.
 *
 * A copy of the MIT License is included in this file.
 *
 *
 * Terms of the MIT License:
 * ---------------------------------------------------
 * Permission is hereby granted, free of charge, to any person obtaining a copy of this software and associated
 * documentation files (the "Software"), to deal in the Software without restriction, including without limitation the
 * rights to use, copy, modify, merge, publish, distribute, sublicense, and/or sell copies of the Software, and to
 * permit persons to whom the Software is furnished to do so, subject to the following conditions:
 *
 * The above copyright notice and this permission notice shall be included in all copies or substantial portions of
 * the Software.
 *
 * THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR IMPLIED, INCLUDING BUT NOT
 * LIMITED TO THE WARRANTIES OF MERCHANTABILITY, FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN
 * NO EVENT SHALL THE AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER LIABILITY,
 * WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM, OUT OF OR IN CONNECTION WITH THE
 * SOFTWARE OR THE USE OR OTHER DEALINGS IN THE SOFTWARE.
 */

package com.tencent.devops.common.sdk.github

import com.tencent.devops.common.sdk.util.SdkHttpUtil
import okhttp3.Credentials
import org.slf4j.LoggerFactory

open class DefaultGithubClient(
    // github服务域名
    open val serverUrl: String,
    // github接口地址
    open val apiUrl: String
) : GithubClient {
    companion object {
        private val logger = LoggerFactory.getLogger(DefaultGithubClient::class.java)
    }

    override fun <T> execute(oauthToken: String, request: GithubRequest<T>): T {
        val headers = mutableMapOf(
            "Authorization" to "token  $oauthToken",
            "Accept" to "application/vnd.github.v3+json"
        )
        return SdkHttpUtil.execute(
            apiUrl = apiUrl,
            systemHeaders = headers,
            request = request
        )
    }

    override fun <T> execute(username: String, token: String, request: GithubRequest<T>): T {
        val headers = mutableMapOf(
            "Authorization" to Credentials.basic(username, token),
            "Accept" to "application/vnd.github.v3+json"
        )
        return SdkHttpUtil.execute(
            apiUrl = apiUrl,
            systemHeaders = headers,
            request = request
        )
    }
}
