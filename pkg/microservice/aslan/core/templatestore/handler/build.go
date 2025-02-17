/*
Copyright 2022 The KodeRover Authors.

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
	"encoding/json"
	"fmt"

	"github.com/gin-gonic/gin"

	commonmodels "github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/models"
	templateservice "github.com/koderover/zadig/pkg/microservice/aslan/core/templatestore/service"
	internalhandler "github.com/koderover/zadig/pkg/shared/handler"
	e "github.com/koderover/zadig/pkg/tool/errors"
)

func GetBuildTemplate(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	// authorization check
	if !ctx.Resources.IsSystemAdmin {
		if !ctx.Resources.SystemActions.Template.View {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Resp, ctx.Err = templateservice.GetBuildTemplateByID(c.Param("id"))
}

func ListBuildTemplates(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	// TODO: Authorization leak
	// authorization check
	//if !ctx.Resources.IsSystemAdmin {
	//	if !ctx.Resources.SystemActions.Template.View {
	//		ctx.UnAuthorized = true
	//		return
	//	}
	//}

	args := &listYamlQuery{}
	if err := c.ShouldBindQuery(args); err != nil {
		ctx.Err = err
		return
	}

	ctx.Resp, ctx.Err = templateservice.ListBuildTemplates(args.PageNum, args.PageSize)
}

func AddBuildTemplate(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	args := new(commonmodels.BuildTemplate)
	err = c.BindJSON(args)
	if err != nil {
		ctx.Err = e.ErrInvalidParam.AddDesc("invalid Build args")
		return
	}

	bs, _ := json.Marshal(args)
	internalhandler.InsertOperationLog(c, ctx.UserName, "", "新增", "模板-构建", args.Name, string(bs), ctx.Logger)

	// authorization check
	if !ctx.Resources.IsSystemAdmin {
		if !ctx.Resources.SystemActions.Template.Create {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = templateservice.AddBuildTemplate(ctx.UserName, args, ctx.Logger)
}

func UpdateBuildTemplate(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	args := new(commonmodels.BuildTemplate)
	err = c.BindJSON(args)
	if err != nil {
		ctx.Err = e.ErrInvalidParam.AddDesc("invalid Build args")
		return
	}

	bs, _ := json.Marshal(args)
	internalhandler.InsertOperationLog(c, ctx.UserName, "", "更新", "模板-构建", args.Name, string(bs), ctx.Logger)

	// authorization check
	if !ctx.Resources.IsSystemAdmin {
		if !ctx.Resources.SystemActions.Template.Edit {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = templateservice.UpdateBuildTemplate(c.Param("id"), args, ctx.Logger)
}

func RemoveBuildTemplate(c *gin.Context) {
	ctx, err := internalhandler.NewContextWithAuthorization(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	if err != nil {
		ctx.Logger.Errorf("failed to generate authorization info for user: %s, error: %s", ctx.UserID, err)
		ctx.Err = fmt.Errorf("authorization Info Generation failed: err %s", err)
		ctx.UnAuthorized = true
		return
	}

	internalhandler.InsertOperationLog(c, ctx.UserName, "", "删除", "模板-构建", c.Param("id"), "", ctx.Logger)

	// authorization check
	if !ctx.Resources.IsSystemAdmin {
		if !ctx.Resources.SystemActions.Template.Delete {
			ctx.UnAuthorized = true
			return
		}
	}

	ctx.Err = templateservice.RemoveBuildTemplate(c.Param("id"), ctx.Logger)
}

func GetBuildTemplateReference(c *gin.Context) {
	ctx := internalhandler.NewContext(c)
	defer func() { internalhandler.JSONResponse(c, ctx) }()

	ctx.Resp, ctx.Err = templateservice.GetBuildTemplateReference(c.Param("id"), ctx.Logger)
}
