/*
Copyright 2021 The KodeRover Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package rest

import (
	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	swaggerfiles "github.com/swaggo/files"
	ginswagger "github.com/swaggo/gin-swagger"

	cachehandler "github.com/koderover/zadig/pkg/handler/cache"
	buildhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/build/handler"
	codehosthandler "github.com/koderover/zadig/pkg/microservice/aslan/core/code/handler"
	collaborationhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/collaboration/handler"
	commonhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/common/handler"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/service/workflowcontroller"
	cronhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/cron/handler"
	deliveryhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/delivery/handler"
	environmenthandler "github.com/koderover/zadig/pkg/microservice/aslan/core/environment/handler"
	labelhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/label/handler"
	loghandler "github.com/koderover/zadig/pkg/microservice/aslan/core/log/handler"
	multiclusterhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/multicluster/handler"
	projecthandler "github.com/koderover/zadig/pkg/microservice/aslan/core/project/handler"
	releaseplanhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/release_plan/handler"
	servicehandler "github.com/koderover/zadig/pkg/microservice/aslan/core/service/handler"
	stathandler "github.com/koderover/zadig/pkg/microservice/aslan/core/stat/handler"
	systemhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/system/handler"
	templatehandler "github.com/koderover/zadig/pkg/microservice/aslan/core/templatestore/handler"
	workflowhandler "github.com/koderover/zadig/pkg/microservice/aslan/core/workflow/handler"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/workflow/service/workflow"
	testinghandler "github.com/koderover/zadig/pkg/microservice/aslan/core/workflow/testing/handler"
	evaluationhandler "github.com/koderover/zadig/pkg/microservice/picket/core/evaluation/handler"
	filterhandler "github.com/koderover/zadig/pkg/microservice/picket/core/filter/handler"
	publichandler "github.com/koderover/zadig/pkg/microservice/picket/core/public/handler"
	podexecservice "github.com/koderover/zadig/pkg/microservice/podexec/core/service"
	policyhandler "github.com/koderover/zadig/pkg/microservice/policy/core/handler"
	configcodehostHandler "github.com/koderover/zadig/pkg/microservice/systemconfig/core/codehost/handler"
	connectorHandler "github.com/koderover/zadig/pkg/microservice/systemconfig/core/connector/handler"
	emailHandler "github.com/koderover/zadig/pkg/microservice/systemconfig/core/email/handler"
	featuresHandler "github.com/koderover/zadig/pkg/microservice/systemconfig/core/features/handler"
	userHandler "github.com/koderover/zadig/pkg/microservice/user/core/handler"
	"github.com/koderover/zadig/pkg/tool/metrics"

	// Note: have to load docs for swagger to work. See https://blog.csdn.net/weixin_43249914/article/details/103035711
	_ "github.com/koderover/zadig/pkg/microservice/aslan/server/rest/doc"
)

func init() {
	// initialization for prometheus metrics
	metrics.Metrics = prometheus.NewRegistry()

	metrics.Metrics.MustRegister(metrics.RunningWorkflows)
	metrics.Metrics.MustRegister(metrics.PendingWorkflows)
	metrics.Metrics.MustRegister(metrics.RequestTotal)
	metrics.Metrics.MustRegister(metrics.CPU)
	metrics.Metrics.MustRegister(metrics.Memory)
	metrics.Metrics.MustRegister(metrics.ResponseTime)

	metrics.UpdatePodMetrics()
}

// @title Zadig aslan service REST APIs
// @version 1.0
// @description The API doc is targeting for Zadig developers rather than Zadig users.
// @description The majority of these APIs are not designed for public use, there is no guarantee on version compatibility.
// @description Please reach out to contact@koderover.com before you decide to depend on these APIs directly.
// @contact.email contact@koderover.com
// @license.name Apache 2.0
// @license.url http://www.apache.org/licenses/LICENSE-2.0.html
func (s *engine) injectRouterGroup(router *gin.RouterGroup) {
	// ---------------------------------------------------------------------------------------
	// 对外公共接口
	// ---------------------------------------------------------------------------------------
	public := router.Group("/api")
	{
		public.POST("/webhook", func(c *gin.Context) {
			c.Request.URL.Path = "/api/workflow/webhook"
			s.HandleContext(c)
		})
		public.GET("/health", commonhandler.Health)
		public.POST("/callback", commonhandler.HandleCallback)
	}

	for name, r := range map[string]injector{
		"/openapi/statistics":   new(stathandler.OpenAPIRouter),
		"/openapi/projects":     new(projecthandler.OpenAPIRouter),
		"/openapi/system":       new(systemhandler.OpenAPIRouter),
		"/openapi/workflows":    new(workflowhandler.OpenAPIRouter),
		"/openapi/environments": new(environmenthandler.OpenAPIRouter),
		"/openapi/quality":      new(testinghandler.QualityRouter),
		"/openapi/build":        new(buildhandler.OpenAPIRouter),
		"/openapi/service":      new(servicehandler.OpenAPIRouter),
	} {
		r.Inject(router.Group(name))
	}

	// no auth required
	router.GET("/api/hub/connect", multiclusterhandler.ClusterConnectFromAgent)

	router.GET("/api/kodespace/downloadUrl", commonhandler.GetToolDownloadURL)

	// inject aslan related APIs
	for name, r := range map[string]injector{
		"/api/project":       new(projecthandler.Router),
		"/api/code":          new(codehosthandler.Router),
		"/api/system":        new(systemhandler.Router),
		"/api/service":       new(servicehandler.Router),
		"/api/environment":   new(environmenthandler.Router),
		"/api/cron":          new(cronhandler.Router),
		"/api/workflow":      new(workflowhandler.Router),
		"/api/release_plan":  new(releaseplanhandler.Router),
		"/api/build":         new(buildhandler.Router),
		"/api/delivery":      new(deliveryhandler.Router),
		"/api/logs":          new(loghandler.Router),
		"/api/testing":       new(testinghandler.Router),
		"/api/cluster":       new(multiclusterhandler.Router),
		"/api/template":      new(templatehandler.Router),
		"/api/collaboration": new(collaborationhandler.Router),
		"/api/label":         new(labelhandler.Router),
		"/api/stat":          new(stathandler.Router),
		"/api/cache":         cachehandler.NewRouter(),
	} {
		r.Inject(router.Group(name))
	}

	// inject config related APIs
	for _, r := range []injector{
		new(connectorHandler.Router),
		new(emailHandler.Router),
		new(configcodehostHandler.Router),
		new(featuresHandler.Router),
	} {
		r.Inject(router.Group("/api/v1"))
	}

	// inject user service APIs
	for _, r := range []injector{
		new(userHandler.Router),
	} {
		r.Inject(router.Group("/api/v1"))
	}

	// inject podexec service API(s)
	podexec := router.Group("/api/podexec")
	{
		podexec.GET("/:productName/:podName/:containerName/podExec/:envName", podexecservice.ServeWs)
		podexec.GET("/production/:productName/:podName/:containerName/podExec/:envName", podexecservice.ServeWs)
		podexec.GET("/debug/:workflowName/:jobName/task/:taskID", podexecservice.DebugWorkflow)
	}

	// inject policy APIs
	for _, r := range []injector{
		new(policyhandler.Router),
	} {
		r.Inject(router.Group("/api/v1"))
	}

	// inject picket APIs
	for _, r := range []injector{
		new(evaluationhandler.Router),
		new(filterhandler.Router),
	} {
		r.Inject(router.Group("/api/v1/picket"))
	}

	for _, r := range []injector{
		new(publichandler.Router),
	} {
		r.Inject(router.Group("/public-api/v1"))
	}

	router.GET("/api/apidocs/*any", ginswagger.WrapHandler(swaggerfiles.Handler))

	// prometheus metrics API
	handlefunc := func(c *gin.Context) {
		metrics.UpdatePodMetrics()

		runningQueue := workflow.RunningTasks()
		pendingQueue := workflow.PendingTasks()
		runningCustomQueue := workflowcontroller.RunningTasks()
		pendingCustomQueue := workflowcontroller.PendingTasks()

		metrics.SetRunningWorkflows(int64(len(runningQueue) + len(runningCustomQueue)))
		metrics.SetPendingWorkflows(int64(len(pendingQueue) + len(pendingCustomQueue)))

		promhttp.HandlerFor(metrics.Metrics, promhttp.HandlerOpts{}).ServeHTTP(c.Writer, c.Request)
	}
	router.GET("/api/metrics", handlefunc)
}

type injector interface {
	Inject(router *gin.RouterGroup)
}
