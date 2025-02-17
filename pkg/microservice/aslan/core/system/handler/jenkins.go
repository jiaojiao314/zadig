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

package handler

import (
	"fmt"
	"net/url"

	"github.com/gin-gonic/gin"

	commonmodels "github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/models"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/system/service"
	internalhandler "github.com/koderover/zadig/pkg/shared/handler"
	e "github.com/koderover/zadig/pkg/tool/errors"
)

type CheckJenkinsIntegrationResp struct {
	Exists bool `json:"exists"`
}

func CheckJenkinsIntegration(c *gin.Context) {
	ctx := internalhandler.NewContext(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	resp, err := service.ListJenkinsIntegration("", ctx.Logger)
	if err != nil || len(resp) == 0 {
		ctx.Resp = &CheckJenkinsIntegrationResp{Exists: false}
		return
	}
	ctx.Resp = &CheckJenkinsIntegrationResp{Exists: true}
}

func CreateJenkinsIntegration(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	args := new(commonmodels.JenkinsIntegration)
	if err := c.BindJSON(args); err != nil {
		ctx.Err = e.ErrInvalidParam.AddDesc("invalid jenkinsIntegration json args")
		return
	}

	// authorization checks
	if !ctx.Resources.IsSystemAdmin {
		ctx.UnAuthorized = true
		return
	}

	args.UpdateBy = ctx.UserName
	if _, err := url.Parse(args.URL); err != nil {
		ctx.Err = e.ErrInvalidParam.AddDesc("invalid url")
		return
	}
	ctx.Err = service.CreateJenkinsIntegration(args, ctx.Logger)
}

func ListJenkinsIntegration(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	// authorization checks
	if !ctx.Resources.IsSystemAdmin {
		ctx.UnAuthorized = true
		return
	}

	encryptedKey := c.Query("encryptedKey")
	if len(encryptedKey) == 0 {
		ctx.Err = e.ErrInvalidParam
		return
	}
	ctx.Resp, ctx.Err = service.ListJenkinsIntegration(encryptedKey, ctx.Logger)
}

func UpdateJenkinsIntegration(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	args := new(commonmodels.JenkinsIntegration)
	if err := c.BindJSON(args); err != nil {
		ctx.Err = e.ErrInvalidParam.AddDesc("invalid jenkinsIntegration json args")
		return
	}

	// authorization checks
	if !ctx.Resources.IsSystemAdmin {
		ctx.UnAuthorized = true
		return
	}

	args.UpdateBy = ctx.UserName
	ctx.Err = service.UpdateJenkinsIntegration(c.Param("id"), args, ctx.Logger)
}

func DeleteJenkinsIntegration(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	// authorization checks
	if !ctx.Resources.IsSystemAdmin {
		ctx.UnAuthorized = true
		return
	}

	ctx.Err = service.DeleteJenkinsIntegration(c.Param("id"), ctx.Logger)
}

func TestJenkinsConnection(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	args := new(service.JenkinsArgs)
	if err := c.BindJSON(args); err != nil {
		ctx.Err = e.ErrInvalidParam.AddDesc("invalid jenkinsArgs json args")
		return
	}

	// authorization checks
	if !ctx.Resources.IsSystemAdmin {
		ctx.UnAuthorized = true
		return
	}

	ctx.Err = service.TestJenkinsConnection(args, ctx.Logger)
}

func ListJobNames(c *gin.Context) {
	ctx := internalhandler.NewContext(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	ctx.Resp, ctx.Err = service.ListJobNames(c.Param("id"), ctx.Logger)
}

func ListJobBuildArgs(c *gin.Context) {
	ctx := internalhandler.NewContext(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	ctx.Resp, ctx.Err = service.ListJobBuildArgs(c.Param("id"), c.Param("jobName"), ctx.Logger)
}
