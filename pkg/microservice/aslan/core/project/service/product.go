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

package service

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/hashicorp/go-multierror"
	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/mongo"
	"go.uber.org/zap"
	"k8s.io/apimachinery/pkg/util/sets"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/yaml"

	"github.com/koderover/zadig/pkg/microservice/aslan/config"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/collaboration/repository/mongodb"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/collaboration/service"
	commonmodels "github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/models"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/models/template"
	commonrepo "github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/mongodb"
	templaterepo "github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/mongodb/template"
	commonservice "github.com/koderover/zadig/pkg/microservice/aslan/core/common/service"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/service/collaboration"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/service/render"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/service/repository"
	commontypes "github.com/koderover/zadig/pkg/microservice/aslan/core/common/types"
	commonutil "github.com/koderover/zadig/pkg/microservice/aslan/core/common/util"
	environmentservice "github.com/koderover/zadig/pkg/microservice/aslan/core/environment/service"
	service2 "github.com/koderover/zadig/pkg/microservice/aslan/core/label/service"
	"github.com/koderover/zadig/pkg/setting"
	"github.com/koderover/zadig/pkg/shared/client/policy"
	kubeclient "github.com/koderover/zadig/pkg/shared/kube/client"
	e "github.com/koderover/zadig/pkg/tool/errors"
	"github.com/koderover/zadig/pkg/tool/kube/getter"
	"github.com/koderover/zadig/pkg/tool/log"
	"github.com/koderover/zadig/pkg/types"
	"github.com/koderover/zadig/pkg/util"
)

type CustomParseDataArgs struct {
	Rules []*ImageParseData `json:"rules"`
}

type ImageParseData struct {
	Repo     string `json:"repo,omitempty"`
	Image    string `json:"image,omitempty"`
	Tag      string `json:"tag,omitempty"`
	InUse    bool   `json:"inUse,omitempty"`
	PresetId int    `json:"presetId,omitempty"`
}

func GetProductTemplateServices(productName string, envType types.EnvType, isBaseEnv bool, baseEnvName string, log *zap.SugaredLogger) (*template.Product, error) {
	resp, err := templaterepo.NewProductColl().Find(productName)
	if err != nil {
		log.Errorf("GetProductTemplate error: %v", err)
		return nil, e.ErrGetProduct.AddDesc(err.Error())
	}

	resp.Services = filterProductServices(productName, resp.Services, false)
	resp.ProductionServices = filterProductServices(productName, resp.ProductionServices, true)

	if envType == types.ShareEnv && !isBaseEnv {
		// At this point the request is from the environment share.
		resp.Services, err = environmentservice.GetEnvServiceList(context.TODO(), productName, baseEnvName)
		if err != nil {
			return nil, fmt.Errorf("failed to get service list from env %s of product %s: %s", baseEnvName, productName, err)
		}
	}

	return resp, nil
}

// Deprecated
func ListOpenSourceProduct(log *zap.SugaredLogger) ([]*template.Product, error) {
	opt := &templaterepo.ProductListOpt{
		IsOpensource: "true",
	}

	tmpls, err := templaterepo.NewProductColl().ListWithOption(opt)
	if err != nil {
		log.Errorf("ProductTmpl.ListWithOpt error: %v", err)
		return nil, e.ErrListProducts.AddDesc(err.Error())
	}

	return tmpls, nil
}

// CreateProductTemplate 创建产品模板
func CreateProductTemplate(args *template.Product, log *zap.SugaredLogger) (err error) {
	kvs := args.Vars
	// do not save vars
	args.Vars = nil

	err = render.ValidateKVs(kvs, args.AllTestServiceInfos(), log)
	if err != nil {
		return e.ErrCreateProduct.AddDesc(err.Error())
	}

	if err := ensureProductTmpl(args); err != nil {
		return e.ErrCreateProduct.AddDesc(err.Error())
	}

	err = commonrepo.NewProjectClusterRelationColl().Delete(&commonrepo.ProjectClusterRelationOption{ProjectName: args.ProductName})
	if err != nil {
		log.Errorf("Failed to delete projectClusterRelation, err:%s", err)
	}
	for _, clusterID := range args.ClusterIDs {
		err = commonrepo.NewProjectClusterRelationColl().Create(&commonmodels.ProjectClusterRelation{
			ProjectName: args.ProductName,
			ClusterID:   clusterID,
			CreatedBy:   args.UpdateBy,
		})
		if err != nil {
			log.Errorf("Failed to create projectClusterRelation, err:%s", err)
		}
	}

	err = templaterepo.NewProductColl().Create(args)
	if err != nil {
		log.Errorf("ProductTmpl.Create error: %v", err)
		return e.ErrCreateProduct.AddDesc(err.Error())
	}

	// add project to current project group
	if args.GroupName != "" {
		err = AddProject2CurrentGroup(args.GroupName, args.ProductName, args.ProjectName, args.UpdateBy, args.ProductFeature.DeployType)
		if err != nil {
			log.Errorf("failed to add project to current group, error: %v", err)
			return e.ErrCreateProduct.AddErr(fmt.Errorf("create project successfully, but failed to add project to current group, please add the project %s to group %s manually, error: %v", args.ProductName, args.GroupName, err))
		}
	}
	return
}

func validateSvc(services [][]string, validServices sets.String) error {
	usedServiceSet := sets.NewString()
	for _, serviceSeq := range services {
		for _, singleSvc := range serviceSeq {
			if usedServiceSet.Has(singleSvc) {
				return fmt.Errorf("duplicated service:%s", singleSvc)
			}
			if !validServices.Has(singleSvc) {
				return fmt.Errorf("invalid service:%s", singleSvc)
			}
			usedServiceSet.Insert(singleSvc)
			validServices.Delete(singleSvc)
		}
	}
	if validServices.Len() > 0 {
		return fmt.Errorf("service: [%s] not found in params", strings.Join(validServices.List(), ","))
	}
	return nil
}

func UpdateServiceOrchestration(name string, services [][]string, updateBy string, log *zap.SugaredLogger) (err error) {
	templateProductInfo, err := templaterepo.NewProductColl().Find(name)
	if err != nil {
		log.Errorf("failed to query productInfo, projectName: %s, err: %s", name, err)
		return fmt.Errorf("failed to query productInfo, projectName: %s", name)
	}

	//validate services
	validServices := sets.NewString()
	for _, serviceList := range templateProductInfo.Services {
		validServices.Insert(serviceList...)
	}

	err = validateSvc(services, validServices)
	if err != nil {
		return e.ErrUpdateProduct.AddErr(err)
	}

	if err = templaterepo.NewProductColl().UpdateServiceOrchestration(name, services, updateBy); err != nil {
		log.Errorf("UpdateChoreographyService error: %v", err)
		return e.ErrUpdateProduct.AddErr(err)
	}
	return nil
}

func UpdateProductionServiceOrchestration(name string, services [][]string, updateBy string, log *zap.SugaredLogger) (err error) {
	templateProductInfo, err := templaterepo.NewProductColl().Find(name)
	if err != nil {
		log.Errorf("failed to query productInfo, projectName: %s, err: %s", name, err)
		return fmt.Errorf("failed to query productInfo, projectName: %s", name)
	}

	//validate services
	validServices := sets.NewString()
	for _, serviceList := range templateProductInfo.ProductionServices {
		validServices.Insert(serviceList...)
	}

	err = validateSvc(services, validServices)
	if err != nil {
		return e.ErrUpdateProduct.AddErr(err)
	}

	if err = templaterepo.NewProductColl().UpdateProductionServiceOrchestration(name, services, updateBy); err != nil {
		log.Errorf("UpdateChoreographyService error: %v", err)
		return e.ErrUpdateProduct.AddErr(err)
	}
	return nil
}

// UpdateProductTemplate 更新产品模板
func UpdateProductTemplate(name string, args *template.Product, log *zap.SugaredLogger) (err error) {
	kvs := args.Vars
	args.Vars = nil

	if err = render.ValidateKVs(kvs, args.AllTestServiceInfos(), log); err != nil {
		log.Warnf("ProductTmpl.Update ValidateKVs error: %v", err)
	}

	if err := ensureProductTmpl(args); err != nil {
		return e.ErrUpdateProduct.AddDesc(err.Error())
	}

	if err = templaterepo.NewProductColl().Update(name, args); err != nil {
		log.Errorf("ProductTmpl.Update error: %v", err)
		return e.ErrUpdateProduct
	}
	// 如果是helm的项目，不需要新创建renderset
	if args.ProductFeature != nil && args.ProductFeature.DeployType == setting.HelmDeployType {
		return
	}

	for _, envVars := range args.EnvVars {
		//创建环境变量
		if err = render.CreateRenderSet(&commonmodels.RenderSet{
			EnvName:     envVars.EnvName,
			Name:        args.ProductName,
			ProductTmpl: args.ProductName,
			UpdateBy:    args.UpdateBy,
			IsDefault:   false,
			//KVs:         envVars.Vars,
		}, log); err != nil {
			log.Warnf("ProductTmpl.Update CreateRenderSet error: %v", err)
		}
	}

	//// 更新子环境渲染集
	//if err = commonservice.UpdateSubRenderSet(args.ProductName, kvs, log); err != nil {
	//	log.Warnf("ProductTmpl.Update UpdateSubRenderSet error: %v", err)
	//}

	return nil
}

// UpdateProductTmplStatus 更新项目onboarding状态
func UpdateProductTmplStatus(productName, onboardingStatus string, log *zap.SugaredLogger) (err error) {
	status, err := strconv.Atoi(onboardingStatus)
	if err != nil {
		log.Errorf("convert onboardingStatus to int failed, error: %v", err)
		return e.ErrUpdateProduct.AddErr(err)
	}

	if err = templaterepo.NewProductColl().UpdateOnboardingStatus(productName, status); err != nil {
		log.Errorf("ProductTmpl.UpdateOnboardingStatus failed, productName:%s, status:%d, error: %v", productName, status, err)
		return e.ErrUpdateProduct.AddErr(err)
	}

	return nil
}

func TransferHostProject(user, projectName string, log *zap.SugaredLogger) (err error) {
	projectInfo, err := templaterepo.NewProductColl().Find(projectName)
	if err != nil {
		return e.ErrUpdateProduct.AddDesc(err.Error())
	}

	if !projectInfo.IsHostProduct() {
		return e.ErrUpdateProduct.AddDesc("invalid project type")
	}

	services, err := transferServices(user, projectInfo, log)
	if err != nil {
		return err
	}

	// transfer host project type to k8s yaml project
	products, err := transferProducts(user, projectInfo, services, log)
	if err != nil {
		return e.ErrUpdateProduct.AddErr(err)
	}

	projectInfo.ProductFeature.CreateEnvType = "system"

	if err = saveServices(projectName, user, services); err != nil {
		return err
	}
	if err = saveProducts(products); err != nil {
		return err
	}
	if err = saveProject(projectInfo, services); err != nil {
		return err
	}
	return nil
}

// transferServices transfer service from external to zadig-host(spock)
func transferServices(user string, projectInfo *template.Product, logger *zap.SugaredLogger) ([]*commonmodels.Service, error) {
	templateServices, err := commonrepo.NewServiceColl().ListMaxRevisionsByProduct(projectInfo.ProductName)
	if err != nil {
		return nil, err
	}

	err = optimizeServiceYaml(projectInfo.ProductName, templateServices)
	if err != nil {
		return nil, err
	}

	for _, svc := range templateServices {
		log.Infof("transfer service %s/%s ", projectInfo.ProductName, svc.ServiceName)
		svc.Source = setting.SourceFromZadig
		svc.CreateBy = user
		svc.EnvName = ""
		svc.WorkloadType = ""
	}
	return templateServices, nil
}

// optimizeServiceYaml optimize the yaml content of service, it removes unnecessary runtime information from workload yamls
// TODO this function should be deleted after we refactor the code about host-project
// CronJob workload is not needed to be handled here since is not supported till version 1.18.0
func optimizeServiceYaml(projectName string, serviceInfo []*commonmodels.Service) error {
	svcMap := make(map[string]*commonmodels.Service)
	svcSets := sets.NewString()
	for _, svc := range serviceInfo {
		svcMap[svc.ServiceName] = svc
		svcSets.Insert(svc.ServiceName)
	}

	products, err := commonrepo.NewProductColl().List(&commonrepo.ProductListOptions{
		Name:       projectName,
		Production: util.GetBoolPointer(false),
	})
	if err != nil {
		return err
	}

	k8sClientMap := make(map[string]client.Client)
	k8sNsMap := make(map[string]string)
	for _, product := range products {
		k8sNsMap[product.EnvName] = product.Namespace
		kubeClient, err := kubeclient.GetKubeClient(config.HubServerAddress(), product.ClusterID)
		if err != nil {
			log.Errorf("failed to init kube client for product %s, err: %s", product.EnvName, err)
			continue
		}
		k8sClientMap[product.EnvName] = kubeClient
	}

	for _, svc := range serviceInfo {
		kClient, ok := k8sClientMap[svc.EnvName]
		if !ok {
			continue
		}

		switch svc.WorkloadType {
		case setting.Deployment:
			bs, exists, err := getter.GetDeploymentYamlFormat(k8sNsMap[svc.EnvName], svc.ServiceName, kClient)
			if err != nil {
				log.Errorf("failed to get deploy %s, err: %s", svc.ServiceName, err)
				continue
			}
			if !exists {
				log.Infof("deployment %s not exists", svc.ServiceName)
				continue
			}
			log.Infof("optimize yaml of deployment %s defined in services", svc.ServiceName)
			svcSets.Delete(svc.ServiceName)
			svc.Yaml = string(bs)
		case setting.StatefulSet:
			bs, exists, err := getter.GetStatefulSetYaml(k8sNsMap[svc.EnvName], svc.ServiceName, kClient)
			if err != nil {
				log.Errorf("failed to get sts %s, err: %s", svc.ServiceName, err)
				continue
			}
			if !exists {
				continue
			}
			log.Infof("optimize yaml of sts %s defined in services", svc.ServiceName)
			svcSets.Delete(svc.ServiceName)
			svc.Yaml = string(bs)
		}
	}

	if svcSets.Len() == 0 {
		return nil
	}

	// service info may be stored in service_in_external_env
	servicesInExternalEnv, _ := commonrepo.NewServicesInExternalEnvColl().List(&commonrepo.ServicesInExternalEnvArgs{
		ProductName: projectName,
	})
	for _, svcInExternal := range servicesInExternalEnv {
		if !svcSets.Has(svcInExternal.ServiceName) {
			continue
		}

		kClient, ok := k8sClientMap[svcInExternal.EnvName]
		if !ok {
			continue
		}

		svc := svcMap[svcInExternal.ServiceName]
		switch svc.WorkloadType {
		case setting.Deployment:
			bs, exists, err := getter.GetDeploymentYamlFormat(k8sNsMap[svcInExternal.EnvName], svcInExternal.ServiceName, kClient)
			if err != nil {
				log.Errorf("failed to get deploy %s, err: %s", svcInExternal.ServiceName, err)
				continue
			}
			if !exists {
				continue
			}
			log.Infof("optimize yaml of deployment %s defined in service_in_external_env", svcInExternal.ServiceName)
			svcSets.Delete(svcInExternal.ServiceName)
			svc.Yaml = string(bs)
		case setting.StatefulSet:
			bs, exists, err := getter.GetStatefulSetYaml(k8sNsMap[svcInExternal.EnvName], svcInExternal.ServiceName, kClient)
			if err != nil {
				log.Errorf("failed to get sts %s, err: %s", svcInExternal.ServiceName, err)
				continue
			}
			if !exists {
				continue
			}
			log.Infof("optimize yaml of sts %s defined in service_in_external_env", svcInExternal.ServiceName)
			svcSets.Delete(svcInExternal.ServiceName)
			svc.Yaml = string(bs)
		}
	}
	return nil
}

func saveServices(projectName, username string, services []*commonmodels.Service) error {
	for _, svc := range services {
		serviceTemplateCounter := fmt.Sprintf(setting.ServiceTemplateCounterName, svc.ServiceName, svc.ProductName)
		err := commonrepo.NewCounterColl().UpsertCounter(serviceTemplateCounter, svc.Revision)
		if err != nil {
			log.Errorf("failed to set service counter: %s, err: %s", serviceTemplateCounter, err)
		}
		err = commonrepo.NewServiceColl().TransferServiceSource(projectName, svc.ServiceName, setting.SourceFromExternal, setting.SourceFromZadig, username, svc.Yaml)
		if err != nil {
			return err
		}
	}
	return nil
}

func saveProducts(products []*commonmodels.Product) error {
	for _, product := range products {

		err := commonrepo.NewServicesInExternalEnvColl().Delete(&commonrepo.ServicesInExternalEnvArgs{
			ProductName: product.ProductName,
			EnvName:     product.EnvName,
		})
		if err != nil && err != mongo.ErrNoDocuments {
			return err
		}

		err = commonrepo.NewProductColl().Update(product)
		if err != nil {
			return err
		}
		saveWorkloadStats(product.ClusterID, product.Namespace, product.ProductName, product.EnvName)
	}
	return nil
}

func saveProject(projectInfo *template.Product, services []*commonmodels.Service) error {
	validServices := sets.NewString()
	for _, svc := range services {
		validServices.Insert(svc.ServiceName)
	}
	projectInfo.Services = [][]string{validServices.List()}
	return templaterepo.NewProductColl().UpdateProductFeatureAndServices(projectInfo.ProductName, projectInfo.ProductFeature, projectInfo.Services, projectInfo.UpdateBy)
}

// build service and env data
func transferProducts(user string, projectInfo *template.Product, templateServices []*commonmodels.Service, logger *zap.SugaredLogger) ([]*commonmodels.Product, error) {
	templateSvcMap := make(map[string]*commonmodels.Service)
	for _, svc := range templateServices {
		templateSvcMap[svc.ServiceName] = svc
	}

	products, err := commonrepo.NewProductColl().List(&commonrepo.ProductListOptions{
		Name: projectInfo.ProductName,
	})
	if err != nil {
		return nil, err
	}

	// build rendersets and services, set necessary attributes
	for _, product := range products {
		rendersetInfo := &commonmodels.RenderSet{
			Name:        product.Namespace,
			EnvName:     product.EnvName,
			ProductTmpl: product.ProductName,
			UpdateBy:    user,
			IsDefault:   false,
		}
		err = render.CreateRenderSet(rendersetInfo, logger)
		if err != nil {
			return nil, err
		}

		product.Render = &commonmodels.RenderInfo{
			Name:        rendersetInfo.Name,
			Revision:    rendersetInfo.Revision,
			ProductTmpl: rendersetInfo.ProductTmpl,
			Description: rendersetInfo.Description,
		}

		currentWorkloads, err := commonservice.ListWorkloadTemplate(projectInfo.ProductName, product.EnvName, logger)
		if err != nil {
			return nil, err
		}

		// for transferred services, only support one group
		productServices := make([]*commonmodels.ProductService, 0)
		for _, workload := range currentWorkloads.Data {
			svcTemplate, ok := templateSvcMap[workload.Service]
			if !ok {
				log.Errorf("failed to find service: %s in template", workload.Service)
			}
			productServices = append(productServices, &commonmodels.ProductService{
				ServiceName: workload.Service,
				ProductName: product.ProductName,
				Type:        svcTemplate.Type,
				Revision:    svcTemplate.Revision,
				Containers:  svcTemplate.Containers,
			})
		}
		product.Services = [][]*commonmodels.ProductService{productServices}

		// update workload stat
		workloadStat, err := commonrepo.NewWorkLoadsStatColl().Find(product.ClusterID, product.Namespace)
		if err != nil {
			log.Errorf("workflowStat not found error:%s", err)
		}
		if workloadStat != nil {
			workloadStat.Workloads = commonservice.FilterWorkloadsByEnv(workloadStat.Workloads, product.ProductName, product.EnvName)
			if err := commonrepo.NewWorkLoadsStatColl().UpdateWorkloads(workloadStat); err != nil {
				log.Errorf("update workloads fail error:%s", err)
			}
		}

		// mark service as only import
		for _, svc := range product.GetServiceMap() {
			product.ServiceDeployStrategy = commonutil.SetServiceDeployStrategyImport(product.ServiceDeployStrategy, svc.ServiceName)
		}

		product.Source = setting.SourceFromZadig
		product.UpdateBy = user
		product.Revision = 1
		log.Infof("transfer project %s/%s ", projectInfo.ProductName, product.EnvName)
	}

	return products, nil
}

func saveWorkloadStats(clusterID, namespace, productName, envName string) {
	workloadStat, err := commonrepo.NewWorkLoadsStatColl().Find(clusterID, namespace)
	if err != nil {
		log.Errorf("failed to get workload stat data, err: %s", err)
		return
	}

	workloadStat.Workloads = commonservice.FilterWorkloadsByEnv(workloadStat.Workloads, productName, envName)
	if err := commonrepo.NewWorkLoadsStatColl().UpdateWorkloads(workloadStat); err != nil {
		log.Errorf("update workloads fail error:%s", err)
	}
}

// UpdateProject 更新项目
func UpdateProject(name string, args *template.Product, log *zap.SugaredLogger) (err error) {
	err = validateRule(args.CustomImageRule, args.CustomTarRule)
	if err != nil {
		return e.ErrInvalidParam.AddDesc(err.Error())
	}

	args.ProjectNamePinyin, args.ProjectNamePinyinFirstLetter = util.GetPinyinFromChinese(args.ProjectName)
	err = templaterepo.NewProductColl().Update(name, args)
	if err != nil {
		log.Errorf("Project.Update error: %v", err)
		return e.ErrUpdateProduct.AddDesc(err.Error())
	}
	return nil
}

func validateRule(customImageRule *template.CustomRule, customTarRule *template.CustomRule) error {
	var (
		customImageRuleMap map[string]string
		customTarRuleMap   map[string]string
	)
	body, err := json.Marshal(&customImageRule)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(body, &customImageRuleMap); err != nil {
		return err
	}

	for field, ruleValue := range customImageRuleMap {
		if err := validateCommonRule(ruleValue, field, config.ImageResourceType); err != nil {
			return err
		}
	}

	body, err = json.Marshal(&customTarRule)
	if err != nil {
		return err
	}
	if err = json.Unmarshal(body, &customTarRuleMap); err != nil {
		return err
	}
	for field, ruleValue := range customTarRuleMap {
		if err := validateCommonRule(ruleValue, field, config.TarResourceType); err != nil {
			return err
		}
	}

	return nil
}

func validateCommonRule(currentRule, ruleType, deliveryType string) error {
	var (
		imageRegexString = "^[a-z0-9][a-zA-Z0-9-_:.]+$"
		tarRegexString   = "^[a-z0-9][a-zA-Z0-9-_.]+$"
		tagRegexString   = "^[a-z0-9A-Z_][a-zA-Z0-9-_.]+$"
		errMessage       = "contains invalid characters, please check"
	)

	if currentRule == "" {
		return fmt.Errorf("%s can not be empty", ruleType)
	}

	if deliveryType == config.ImageResourceType && !strings.Contains(currentRule, ":") {
		return fmt.Errorf("%s is invalid, must contain a colon", ruleType)
	}

	currentRule = commonservice.ReplaceRuleVariable(currentRule, &commonservice.Variable{
		"ss", "ss", "ss", "ss", "ss", "ss", "ss", "ss", "ss", "ss",
	})
	switch deliveryType {
	case config.ImageResourceType:
		if !regexp.MustCompile(imageRegexString).MatchString(currentRule) {
			return fmt.Errorf("image %s %s", ruleType, errMessage)
		}
		// validate tag
		tag := strings.Split(currentRule, ":")[1]
		if !regexp.MustCompile(tagRegexString).MatchString(tag) {
			return fmt.Errorf("image %s %s", ruleType, errMessage)
		}
	case config.TarResourceType:
		if !regexp.MustCompile(tarRegexString).MatchString(currentRule) {
			return fmt.Errorf("tar %s %s", ruleType, errMessage)
		}
	}
	return nil
}

// DeleteProductTemplate 删除产品模板
func DeleteProductTemplate(userName, productName, requestID string, isDelete bool, log *zap.SugaredLogger) (err error) {
	envs, _ := commonrepo.NewProductColl().List(&commonrepo.ProductListOptions{Name: productName})
	for _, env := range envs {
		if err = commonrepo.NewProductColl().UpdateStatus(env.EnvName, productName, setting.ProductStatusDeleting); err != nil {
			log.Errorf("DeleteProductTemplate Update product Status error: %s", err)
			return e.ErrDeleteProduct
		}
	}

	if err = render.DeleteRenderSet(productName, log); err != nil {
		log.Errorf("DeleteProductTemplate DeleteRenderSet err: %s", err)
		return err
	}

	if err = DeleteTestModules(productName, requestID, log); err != nil {
		log.Errorf("DeleteProductTemplate Delete productName %s test err: %s", productName, err)
		return err
	}

	// delete collaboration_mode and collaboration_instance
	if err := DeleteCollabrationMode(productName, userName, log); err != nil {
		log.Errorf("DeleteCollabrationMode err:%s", err)
		return err
	}

	if err = DeletePolicy(productName, log); err != nil {
		log.Errorf("DeletePolicy  productName %s  err: %s", productName, err)
		return err
	}

	if err = DeleteLabels(productName, log); err != nil {
		log.Errorf("DeleteLabels  productName %s  err: %s", productName, err)
		return err
	}

	if err = commonservice.DeleteWorkflows(productName, requestID, log); err != nil {
		log.Errorf("DeleteProductTemplate Delete productName %s workflow err: %s", productName, err)
		return err
	}

	if err = commonservice.DeleteWorkflowV3s(productName, requestID, log); err != nil {
		log.Errorf("DeleteProductTemplate Delete productName %s workflowV3 err: %s", productName, err)
		return err
	}

	if err = commonservice.DeleteWorkflowV4sByProjectName(productName, log); err != nil {
		log.Errorf("DeleteProductTemplate Delete productName %s workflowV4 err: %s", productName, err)
		return err
	}

	if err = commonservice.DeletePipelines(productName, requestID, log); err != nil {
		log.Errorf("DeleteProductTemplate Delete productName %s pipeline err: %s", productName, err)
		return err
	}

	// delete projectClusterRelation
	if err = commonrepo.NewProjectClusterRelationColl().Delete(&commonrepo.ProjectClusterRelationOption{ProjectName: productName}); err != nil {
		log.Errorf("DeleteProductTemplate Delete productName %s ProjectClusterRelation err: %s", productName, err)
	}

	err = templaterepo.NewProductColl().Delete(productName)
	if err != nil {
		log.Errorf("ProductTmpl.Delete error: %s", err)
		return e.ErrDeleteProduct
	}

	err = commonrepo.NewCounterColl().Delete(fmt.Sprintf("product:%s", productName))
	if err != nil {
		log.Errorf("Counter.Delete error: %s", err)
		return err
	}

	services, _ := commonrepo.NewServiceColl().ListMaxRevisions(
		&commonrepo.ServiceListOption{ProductName: productName, Type: setting.K8SDeployType},
	)

	//删除交付中心
	//删除构建/删除测试/删除服务
	//删除workflow和历史task
	go func() {
		_ = commonrepo.NewBuildColl().Delete("", productName)
		_ = commonrepo.NewServiceColl().Delete("", "", productName, "", 0)
		_ = commonrepo.NewProductionServiceColl().DeleteByProject(productName)
		_ = commonservice.DeleteDeliveryInfos(productName, log)
		_ = DeleteProductsAsync(userName, productName, requestID, isDelete, log)

		// delete service webhooks after services are deleted
		for _, s := range services {
			commonservice.ProcessServiceWebhook(nil, s, s.ServiceName, log)
		}
	}()

	// delete data in workload_stat
	// TODO this function should be removed after workload_stat is deprecated
	go func() {
		workloads, _ := commonrepo.NewWorkLoadsStatColl().FindByProductName(productName)
		for _, v := range workloads {
			// update workloads
			tmp := []commonmodels.Workload{}
			for _, vv := range v.Workloads {
				if vv.ProductName != productName {
					tmp = append(tmp, vv)
				}
			}
			v.Workloads = tmp
			commonrepo.NewWorkLoadsStatColl().UpdateWorkloads(v)
		}
	}()

	// delete servicesInExternalEnv data
	// TODO this function should be removed after services_in_external_env is deprecated
	go func() {
		_ = commonrepo.NewServicesInExternalEnvColl().Delete(&commonrepo.ServicesInExternalEnvArgs{
			ProductName: productName,
		})
	}()

	// delete privateKey data
	go func() {
		if err = commonrepo.NewPrivateKeyColl().BulkDelete(productName); err != nil {
			log.Errorf("failed to bulk delete privateKey, error:%s", err)
		}
	}()

	return nil
}

func filterProductServices(productName string, source [][]string, production bool) [][]string {
	ret := make([][]string, 0)
	if len(source) == 0 {
		return ret
	}
	curServices, err := repository.ListMaxRevisionsServices(productName, production)
	if err != nil {
		log.Errorf("failed to query template services for product %s, production: %v, error: %v", productName, production, err)
		return source
	}

	validSvcSet := sets.NewString()
	for _, svc := range curServices {
		validSvcSet.Insert(svc.ServiceName)
	}

	for _, svcGroup := range source {
		validSvcs := make([]string, 0)
		for _, svc := range svcGroup {
			if validSvcSet.Has(svc) {
				validSvcs = append(validSvcs, svc)
			}
		}
		if len(validSvcs) > 0 {
			ret = append(ret, validSvcs)
		}
	}
	return ret
}

// ensureProductTmpl 检查产品模板参数
func ensureProductTmpl(args *template.Product) error {
	if args == nil {
		return errors.New("nil ProductTmpl")
	}

	if len(args.ProductName) == 0 {
		return errors.New("empty product name")
	}

	if !config.ServiceNameRegex.MatchString(args.ProductName) {
		return fmt.Errorf("product name must match %s", config.ServiceNameRegexString)
	}

	serviceNames := sets.NewString()
	for _, sg := range args.Services {
		for _, s := range sg {
			if serviceNames.Has(s) {
				return fmt.Errorf("duplicated service found: %s", s)
			}
			serviceNames.Insert(s)
		}
	}

	// 设置新的版本号
	rev, err := commonrepo.NewCounterColl().GetNextSeq("product:" + args.ProductName)
	if err != nil {
		return fmt.Errorf("get next product template revision error: %v", err)
	}

	args.Revision = rev

	args.ProjectNamePinyin, args.ProjectNamePinyinFirstLetter = util.GetPinyinFromChinese(args.ProjectName)
	return nil
}

func DeleteProductsAsync(userName, productName, requestID string, isDelete bool, log *zap.SugaredLogger) error {
	envs, err := commonrepo.NewProductColl().List(&commonrepo.ProductListOptions{Name: productName})
	if err != nil {
		return e.ErrListProducts.AddDesc(err.Error())
	}
	errList := new(multierror.Error)
	for _, env := range envs {
		if env.Production {
			err = environmentservice.DeleteProductionProduct(userName, env.EnvName, productName, requestID, log)
		} else {
			err = environmentservice.DeleteProduct(userName, env.EnvName, productName, requestID, isDelete, log)
		}
		if err != nil {
			errList = multierror.Append(errList, err)
		}
	}
	if err := errList.ErrorOrNil(); err != nil {
		log.Errorf("DeleteProductsAsync err:%v", err)
		return err
	}
	return nil
}

type ProductInfo struct {
	Value       string         `bson:"value"              json:"value"`
	Label       string         `bson:"label"              json:"label"`
	ServiceInfo []*ServiceInfo `bson:"services"           json:"services"`
}

type ServiceInfo struct {
	Value         string           `bson:"value"              json:"value"`
	Label         string           `bson:"label"              json:"label"`
	ContainerInfo []*ContainerInfo `bson:"containers"         json:"containers"`
}

// ContainerInfo ...
type ContainerInfo struct {
	Value string `bson:"value"              json:"value"`
	Label string `bson:"label"              json:"label"`
}

func ListTemplatesHierachy(userName string, log *zap.SugaredLogger) ([]*ProductInfo, error) {
	var (
		err          error
		resp         = make([]*ProductInfo, 0)
		productTmpls = make([]*template.Product, 0)
	)

	productTmpls, err = templaterepo.NewProductColl().List()
	if err != nil {
		log.Errorf("[%s] ProductTmpl.List error: %v", userName, err)
		return nil, e.ErrListProducts.AddDesc(err.Error())
	}

	for _, productTmpl := range productTmpls {
		pInfo := &ProductInfo{Value: productTmpl.ProductName, Label: productTmpl.ProductName, ServiceInfo: []*ServiceInfo{}}
		services, err := commonrepo.NewServiceColl().ListMaxRevisionsForServices(productTmpl.AllTestServiceInfos(), "")
		if err != nil {
			log.Errorf("Failed to list service for project %s, error: %s", productTmpl.ProductName, err)
			return nil, e.ErrGetProduct.AddDesc(err.Error())
		}
		for _, svcTmpl := range services {
			sInfo := &ServiceInfo{Value: svcTmpl.ServiceName, Label: svcTmpl.ServiceName, ContainerInfo: make([]*ContainerInfo, 0)}

			for _, c := range svcTmpl.Containers {
				sInfo.ContainerInfo = append(sInfo.ContainerInfo, &ContainerInfo{Value: c.Name, Label: c.Name})
			}

			pInfo.ServiceInfo = append(pInfo.ServiceInfo, sInfo)
		}
		resp = append(resp, pInfo)
	}
	return resp, nil
}

func GetCustomMatchRules(productName string, log *zap.SugaredLogger) ([]*ImageParseData, error) {
	productInfo, err := templaterepo.NewProductColl().Find(productName)
	if err != nil {
		log.Errorf("query product:%s fail, err:%s", productName, err.Error())
		return nil, fmt.Errorf("failed to find product %s", productName)
	}

	rules := productInfo.ImageSearchingRules
	if len(rules) == 0 {
		rules = commonutil.GetPresetRules()
	}

	ret := make([]*ImageParseData, 0, len(rules))
	for _, singleData := range rules {
		ret = append(ret, &ImageParseData{
			Repo:     singleData.Repo,
			Image:    singleData.Image,
			Tag:      singleData.Tag,
			InUse:    singleData.InUse,
			PresetId: singleData.PresetId,
		})
	}
	return ret, nil
}

func UpdateCustomMatchRules(productName, userName, requestID string, matchRules []*ImageParseData) error {
	productInfo, err := templaterepo.NewProductColl().Find(productName)
	if err != nil {
		log.Errorf("query product:%s fail, err:%s", productName, err.Error())
		return fmt.Errorf("failed to find product %s", productName)
	}

	if len(matchRules) == 0 {
		return errors.New("match rules can't be empty")
	}
	haveInUse := false
	for _, rule := range matchRules {
		if rule.InUse {
			haveInUse = true
			break
		}
	}
	if !haveInUse {
		return errors.New("no rule is selected to be used")
	}

	imageRulesToSave := make([]*template.ImageSearchingRule, 0)
	for _, singleData := range matchRules {
		if singleData.Repo == "" && singleData.Image == "" && singleData.Tag == "" {
			continue
		}
		imageRulesToSave = append(imageRulesToSave, &template.ImageSearchingRule{
			Repo:     singleData.Repo,
			Image:    singleData.Image,
			Tag:      singleData.Tag,
			InUse:    singleData.InUse,
			PresetId: singleData.PresetId,
		})
	}

	productInfo.ImageSearchingRules = imageRulesToSave
	productInfo.UpdateBy = userName

	services, err := repository.ListMaxRevisionsServices(productName, false)
	if err != nil {
		return errors.Wrapf(err, "fail to list services of product %s", productName)
	}
	err = reParseServices(userName, requestID, services, imageRulesToSave, false)
	if err != nil {
		return err
	}

	productionServices, err := repository.ListMaxRevisionsServices(productName, true)
	if err != nil {
		return errors.Wrapf(err, "fail to list production services of product %s", productName)
	}
	err = reParseServices(userName, requestID, productionServices, imageRulesToSave, true)
	if err != nil {
		return err
	}

	err = templaterepo.NewProductColl().Update(productName, productInfo)
	if err != nil {
		log.Errorf("failed to update product:%s, err:%s", productName, err.Error())
		return fmt.Errorf("failed to store match rules")
	}

	return nil
}

// reparse values.yaml for each service
func reParseServices(userName, requestID string, serviceList []*commonmodels.Service, matchRules []*template.ImageSearchingRule, production bool) error {
	updatedServiceTmpls := make([]*commonmodels.Service, 0)

	var err error
	var projectName string
	for _, serviceTmpl := range serviceList {
		if serviceTmpl.Type != setting.HelmDeployType || serviceTmpl.HelmChart == nil {
			continue
		}
		valuesYaml := serviceTmpl.HelmChart.ValuesYaml
		projectName = serviceTmpl.ProductName

		valuesMap := make(map[string]interface{})
		err = yaml.Unmarshal([]byte(valuesYaml), &valuesMap)
		if err != nil {
			err = errors.Wrapf(err, "failed to unmarshal values.yamf for service %s", serviceTmpl.ServiceName)
			break
		}

		serviceTmpl.Containers, err = commonutil.ParseImagesByRules(valuesMap, matchRules)
		if err != nil {
			break
		}

		if len(serviceTmpl.Containers) == 0 {
			log.Warnf("service:%s containers is empty after parse, valuesYaml %s", serviceTmpl.ServiceName, valuesYaml)
		}

		serviceTmpl.CreateBy = userName

		// TODO optimize me: use common function to generate nex service revision
		rev, errRevision := commonutil.GenerateServiceNextRevision(production, serviceTmpl.ServiceName, serviceTmpl.ProductName)
		if errRevision != nil {
			err = fmt.Errorf("get next helm service revision error: %v", errRevision)
			break
		}
		serviceTmpl.Revision = rev

		if !production {
			if err = commonrepo.NewServiceColl().Delete(serviceTmpl.ServiceName, setting.HelmDeployType, serviceTmpl.ProductName, setting.ProductStatusDeleting, serviceTmpl.Revision); err != nil {
				log.Errorf("helmService.update delete %s error: %v", serviceTmpl.ServiceName, err)
				break
			}

			if err = commonrepo.NewServiceColl().Create(serviceTmpl); err != nil {
				log.Errorf("helmService.update serviceName:%s error:%v", serviceTmpl.ServiceName, err)
				err = e.ErrUpdateTemplate.AddDesc(err.Error())
				break
			}
		} else {
			if err = commonrepo.NewProductionServiceColl().Delete(serviceTmpl.ServiceName, serviceTmpl.ProductName, setting.ProductStatusDeleting, serviceTmpl.Revision); err != nil {
				log.Errorf("helmService.update delete production service %s error: %v", serviceTmpl.ServiceName, err)
				break
			}

			if err = commonrepo.NewProductionServiceColl().Create(serviceTmpl); err != nil {
				log.Errorf("helmService.update production service, serviceName:%s error:%v", serviceTmpl.ServiceName, err)
				err = e.ErrUpdateTemplate.AddDesc(err.Error())
				break
			}
		}

		updatedServiceTmpls = append(updatedServiceTmpls, serviceTmpl)
	}

	// roll back all template services if error occurs
	if err != nil {
		for _, serviceTmpl := range updatedServiceTmpls {
			if !production {
				if err = commonrepo.NewServiceColl().Delete(serviceTmpl.ServiceName, setting.HelmDeployType, serviceTmpl.ProductName, "", serviceTmpl.Revision); err != nil {
					log.Errorf("helmService.update delete %s error: %v", serviceTmpl.ServiceName, err)
					continue
				}
			} else {
				if err = commonrepo.NewProductionServiceColl().Delete(serviceTmpl.ServiceName, serviceTmpl.ProductName, "", serviceTmpl.Revision); err != nil {
					log.Errorf("helmService.update delete %s error: %v", serviceTmpl.ServiceName, err)
					continue
				}
			}
		}
		return err
	}

	if production {
		return nil
	}
	return environmentservice.AutoDeployHelmServiceToEnvs(userName, requestID, projectName, updatedServiceTmpls, log.SugaredLogger())
}

func DeleteCollabrationMode(productName string, userName string, log *zap.SugaredLogger) error {
	// find all collaboration mode in this project
	res, err := collaboration.GetCollaborationModes([]string{productName}, log)
	if err != nil {
		log.Errorf("GetCollaborationModes err: %s", err)
		return err
	}
	//  delete all collaborationMode
	for _, mode := range res.Collaborations {
		if err := service.DeleteCollaborationMode(userName, productName, mode.Name, log); err != nil {
			log.Errorf("DeleteCollaborationMode err: %s", err)
			return err
		}
	}
	// delete all collaborationIns
	if err := mongodb.NewCollaborationInstanceColl().DeleteByProject(productName); err != nil {
		log.Errorf("fail to DeleteByProject err:%s", err)
		return err
	}
	return nil
}

func DeletePolicy(productName string, log *zap.SugaredLogger) error {
	policy.NewDefault()
	if err := policy.NewDefault().DeletePolicies(productName, policy.DeletePoliciesArgs{
		Names: []string{},
	}); err != nil {
		log.Errorf("DeletePolicies err :%s", err)
		return err
	}
	return nil
}

func DeleteLabels(productName string, log *zap.SugaredLogger) error {
	if err := service2.DeleteLabelsAndBindingsByProject(productName, log); err != nil {
		log.Errorf("delete labels and bindings by project fail , err :%s", err)
		return err
	}
	return nil
}

func GetGlobalVariables(productName string, production bool, log *zap.SugaredLogger) ([]*commontypes.ServiceVariableKV, error) {
	productInfo, err := templaterepo.NewProductColl().Find(productName)
	if err != nil {
		return nil, fmt.Errorf("failed to find product %s, err: %w", productName, err)
	}

	if production {
		return productInfo.ProductionGlobalVariables, nil
	}
	return productInfo.GlobalVariables, nil
}

func UpdateGlobalVariables(productName, userName string, globalVariables []*commontypes.ServiceVariableKV, production bool) error {
	productInfo, err := templaterepo.NewProductColl().Find(productName)
	if err != nil {
		return fmt.Errorf("failed to find product %s, err: %w", productName, err)
	}

	keySet := sets.NewString()
	for _, kv := range globalVariables {
		if keySet.Has(kv.Key) {
			return fmt.Errorf("duplicated key: %s", kv.Key)
		}
		keySet.Insert(kv.Key)
	}

	productInfo.UpdateBy = userName
	if production {
		productInfo.ProductionGlobalVariables = globalVariables
	} else {
		productInfo.GlobalVariables = globalVariables
	}

	err = templaterepo.NewProductColl().Update(productName, productInfo)
	if err != nil {
		return fmt.Errorf("failed to update product: %s, err: %w", productName, err)
	}

	return nil
}

type GetGlobalVariableCandidatesRespone struct {
	KeyName        string   `json:"key_name"`
	RelatedService []string `json:"related_service"`
}

func GetGlobalVariableCandidates(productName string, production bool, log *zap.SugaredLogger) ([]*GetGlobalVariableCandidatesRespone, error) {
	productInfo, err := templaterepo.NewProductColl().Find(productName)
	if err != nil {
		return nil, fmt.Errorf("failed to find product %s, err: %w", productName, err)
	}

	services, err := repository.ListMaxRevisionsServices(productName, production)
	if err != nil {
		return nil, fmt.Errorf("failed to list services by product %s, err: %w", productName, err)
	}

	existedVariableSet := sets.NewString()
	variableMap := make(map[string]*GetGlobalVariableCandidatesRespone)
	if production {
		for _, kv := range productInfo.ProductionGlobalVariables {
			existedVariableSet.Insert(kv.Key)
		}
	} else {
		for _, kv := range productInfo.GlobalVariables {
			existedVariableSet.Insert(kv.Key)
		}
	}

	ret := make([]*GetGlobalVariableCandidatesRespone, 0)
	for _, service := range services {
		for _, kv := range service.ServiceVariableKVs {
			if !existedVariableSet.Has(kv.Key) {
				if candiate, ok := variableMap[kv.Key]; ok {
					candiate.RelatedService = append(candiate.RelatedService, service.ServiceName)
				} else {
					variableMap[kv.Key] = &GetGlobalVariableCandidatesRespone{
						KeyName:        kv.Key,
						RelatedService: []string{service.ServiceName},
					}
				}
			}
		}
	}

	for _, candiate := range variableMap {
		ret = append(ret, candiate)
	}

	return ret, nil
}

func CreateProjectGroup(args *ProjectGroupArgs, user string, logger *zap.SugaredLogger) error {
	groups, err := commonrepo.NewProjectGroupColl().List()
	if err != nil {
		errMsg := fmt.Errorf("failed to list project groups, error: %v", err)
		logger.Errorf(errMsg.Error())
		return e.ErrCreateProjectGroup.AddErr(errMsg)
	}

	// find all project keys that have been set
	set := sets.NewString()
	for _, group := range groups {
		for _, project := range group.Projects {
			set.Insert(project.ProjectKey)
		}
	}

	projects, err := templaterepo.NewProductColl().List()
	if err != nil {
		errMsg := fmt.Errorf("failed to list projects, error: %v", err)
		logger.Errorf(errMsg.Error())
		return e.ErrCreateProjectGroup.AddErr(errMsg)
	}

	pm := make(map[string]*template.Product)
	for _, project := range projects {
		pm[project.ProductName] = project
	}

	group := &commonmodels.ProjectGroup{
		Name:        args.GroupName,
		CreatedTime: time.Now().Unix(),
		UpdateTime:  time.Now().Unix(),
		CreatedBy:   user,
		UpdateBy:    user,
		Projects:    make([]*commonmodels.ProjectDetail, 0),
	}
	for _, project := range args.ProjectKeys {
		if set.Has(project) {
			return e.ErrCreateProjectGroup.AddErr(fmt.Errorf("failed to set project %s to group %s, project Key %s has been set in other groups", project, args.GroupName, project))
		}

		if p, ok := pm[project]; ok {
			group.Projects = append(group.Projects, &commonmodels.ProjectDetail{
				ProjectKey:        p.ProductName,
				ProjectName:       p.ProjectName,
				ProjectDeployType: p.ProductFeature.DeployType,
			})
		} else {
			return e.ErrCreateProjectGroup.AddErr(fmt.Errorf("project Key %s not in current project list", project))
		}
	}

	if err := commonrepo.NewProjectGroupColl().Create(group); err != nil {
		errMsg := fmt.Errorf("failed to create project group, error: %v", err)
		logger.Errorf(errMsg.Error())
		return e.ErrCreateProjectGroup.AddErr(errMsg)
	}
	return nil
}

func UpdateProjectGroup(args *ProjectGroupArgs, user string, logger *zap.SugaredLogger) error {
	if args.GroupID == "" {
		return e.ErrUpdateProjectGroup.AddDesc("group id can not be empty")
	}

	groups, err := commonrepo.NewProjectGroupColl().List()
	if err != nil {
		errMsg := fmt.Errorf("failed to list project groups, error: %v", err)
		logger.Errorf(errMsg.Error())
		return e.ErrCreateProjectGroup.AddErr(errMsg)
	}

	// find all project keys that have been set
	set := sets.NewString()
	for _, group := range groups {
		if group.Name != args.GroupName {
			for _, project := range group.Projects {
				set.Insert(project.ProjectKey)
			}
		}
	}

	projects, err := templaterepo.NewProductColl().List()
	if err != nil {
		errMsg := fmt.Errorf("failed to list projects, error: %v", err)
		logger.Errorf(errMsg.Error())
		return e.ErrUpdateProjectGroup.AddErr(errMsg)
	}

	pm := make(map[string]*template.Product)
	for _, project := range projects {
		pm[project.ProductName] = project
	}
	group := &commonmodels.ProjectGroup{
		Name:       args.GroupName,
		UpdateTime: time.Now().Unix(),
		UpdateBy:   user,
		Projects:   make([]*commonmodels.ProjectDetail, 0),
	}
	for _, project := range args.ProjectKeys {
		if set.Has(project) {
			return e.ErrCreateProjectGroup.AddErr(fmt.Errorf("failed to set project %s to group %s, project Key %s has been set in other groups", project, args.GroupName, project))
		}

		if p, ok := pm[project]; ok {
			group.Projects = append(group.Projects, &commonmodels.ProjectDetail{
				ProjectKey:        p.ProductName,
				ProjectName:       p.ProjectName,
				ProjectDeployType: p.ProductFeature.DeployType,
			})
		} else {
			return e.ErrUpdateProjectGroup.AddErr(fmt.Errorf("project Key %s not in current project list", project))
		}
	}

	oldGroup, err := commonrepo.NewProjectGroupColl().Find(commonrepo.ProjectGroupOpts{ID: args.GroupID})
	if err != nil {
		return e.ErrUpdateProjectGroup.AddErr(fmt.Errorf("failed to find project group %s, error: %v", args.GroupName, err))
	}
	group.ID = oldGroup.ID
	group.CreatedTime = oldGroup.CreatedTime
	group.CreatedBy = oldGroup.CreatedBy

	if err := commonrepo.NewProjectGroupColl().Update(group); err != nil {
		errMsg := fmt.Errorf("failed to update project group, groupName:%s, error: %v", oldGroup.Name, err)
		logger.Errorf(errMsg.Error())
		return e.ErrUpdateProjectGroup.AddErr(errMsg)
	}
	return nil
}

func DeleteProjectGroup(name string, logger *zap.SugaredLogger) error {
	if err := commonrepo.NewProjectGroupColl().Delete(name); err != nil {
		return e.ErrDeleteProjectGroup.AddErr(fmt.Errorf("failed to delete project group %s, error: %v", name, err))
	}
	return nil
}

func ListProjectGroupNames() ([]string, error) {
	groups, err := commonrepo.NewProjectGroupColl().ListGroupNames()
	if err != nil {
		return nil, fmt.Errorf("failed to list project groups, error: %v", err)
	}

	// create the default group ungrouped
	// groups = append(groups, setting.UNGROUPED)

	return groups, nil
}

func GetProjectGroupRelation(name string, logger *zap.SugaredLogger) (resp *ProjectGroupPreset, err error) {
	var group *commonmodels.ProjectGroup
	if name != "" {
		group, err = commonrepo.NewProjectGroupColl().Find(commonrepo.ProjectGroupOpts{Name: name})
		if err != nil {
			return nil, fmt.Errorf("failed to find project group %s, error: %v", name, err)
		}
	}

	resp = &ProjectGroupPreset{
		Projects: make([]*ProjectGroupRelation, 0),
	}
	if group != nil {
		resp.GroupName = group.Name
		resp.GroupID = group.ID.Hex()
	}

	if group != nil {
		for _, project := range group.Projects {
			resp.Projects = append(resp.Projects, &ProjectGroupRelation{
				ProjectKey:  project.ProjectKey,
				ProjectName: project.ProjectName,
				DeployType:  project.ProjectDeployType,
				Enabled:     true,
			})
		}
	}

	unGrouped, err := GetUnGroupedProjectKeys()
	if err != nil {
		return nil, fmt.Errorf("failed to list ungrouped projects, error: %v", err)
	}
	projects, err := templaterepo.NewProductColl().ListProjectBriefs(unGrouped)
	if err != nil {
		return nil, fmt.Errorf("failed to list projects, error: %v", err)
	}

	for _, project := range projects {
		resp.Projects = append(resp.Projects, &ProjectGroupRelation{
			ProjectKey:  project.Name,
			ProjectName: project.Alias,
			DeployType:  project.DeployType,
			Enabled:     false,
		})
	}
	sort.Slice(resp.Projects, func(i, j int) bool {
		return resp.Projects[i].ProjectKey < resp.Projects[j].ProjectKey
	})
	return resp, nil
}

func AddProject2CurrentGroup(groupName, projectKey, projectDisplayName, deployType, user string) error {
	group, err := commonrepo.NewProjectGroupColl().Find(commonrepo.ProjectGroupOpts{Name: groupName})
	if err != nil {
		return fmt.Errorf("failed to find project group %s, error: %v", groupName, err)
	}

	for _, project := range group.Projects {
		if project.ProjectKey == projectKey {
			return nil
		}
	}

	group.Projects = append(group.Projects, &commonmodels.ProjectDetail{
		ProjectKey:        projectKey,
		ProjectName:       projectDisplayName,
		ProjectDeployType: deployType,
	})
	group.UpdateBy = user
	group.UpdateTime = time.Now().Unix()

	if err := commonrepo.NewProjectGroupColl().Update(group); err != nil {
		return fmt.Errorf("failed to update project group %s, error: %v", groupName, err)
	}
	return nil
}

func GetUnGroupedProjectKeys() ([]string, error) {
	groups, err := commonrepo.NewProjectGroupColl().List()
	if err != nil {
		return nil, fmt.Errorf("failed to list project groups, error: %v", err)
	}

	set := sets.NewString()
	for _, group := range groups {
		for _, project := range group.Projects {
			set.Insert(project.ProjectKey)
		}
	}

	projects, err := templaterepo.NewProductColl().ListAllName()
	if err != nil {
		return nil, fmt.Errorf("failed to list projects, error: %v", err)
	}

	unGroupedKeys := make([]string, 0)
	for _, project := range projects {
		if !set.Has(project) {
			unGroupedKeys = append(unGroupedKeys, project)
		}
	}
	return unGroupedKeys, nil
}
