/*
 * Copyright 2023 The KodeRover Authors.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package service

import (
	"fmt"

	"github.com/pkg/errors"
	"github.com/samber/lo"
	"k8s.io/apimachinery/pkg/util/sets"

	"github.com/koderover/zadig/pkg/microservice/aslan/config"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/models"
	jobctl "github.com/koderover/zadig/pkg/microservice/aslan/core/workflow/service/workflow/job"
)

func lintReleaseJob(_type config.ReleasePlanJobType, spec interface{}) error {
	switch _type {
	case config.JobText:
		t := new(models.TextReleaseJobSpec)
		if err := models.IToi(spec, t); err != nil {
			return fmt.Errorf("invalid text spec: %v", err)
		}
		return nil
	case config.JobWorkflow:
		w := new(models.WorkflowReleaseJobSpec)
		if err := models.IToi(spec, w); err != nil {
			return fmt.Errorf("invalid workflow spec: %v", err)
		}
		return lintWorkflow(w.Workflow)
	default:
		return fmt.Errorf("invalid release job type: %s", _type)
	}
}

func lintWorkflow(workflow *models.WorkflowV4) error {
	if workflow == nil {
		return fmt.Errorf("workflow cannot be empty")
	}
	// ToJobs will change raw workflow data, so we need to copy it
	tmp := new(models.WorkflowV4)
	if err := models.IToi(workflow, tmp); err != nil {
		return fmt.Errorf("IToi tmp workflow error: %v", err)
	}
	for _, stage := range tmp.Stages {
		for _, job := range stage.Jobs {
			if jobctl.JobSkiped(job) {
				continue
			}
			err := jobctl.LintJob(job, workflow)
			if err != nil {
				return fmt.Errorf("lint job-%s err: %v", job.Name, err)
			}
			_, err = jobctl.ToJobs(job, workflow, 0)
			if err != nil {
				return fmt.Errorf("lint job-%s runtime err: %v", job.Name, err)
			}
		}
	}
	return nil
}

func lintApproval(approval *models.Approval) error {
	if approval == nil {
		return nil
	}
	if !approval.Enabled {
		return nil
	}
	switch approval.Type {
	case config.NativeApproval:
		if approval.NativeApproval == nil {
			return errors.New("approval not found")
		}
		if len(approval.NativeApproval.ApproveUsers) < approval.NativeApproval.NeededApprovers {
			return errors.New("all approve users should not less than needed approvers")
		}
	case config.LarkApproval:
		if approval.LarkApproval == nil {
			return errors.New("approval not found")
		}
		if len(approval.LarkApproval.ApprovalNodes) == 0 {
			return errors.New("num of approval-node is 0")
		}
		for i, node := range approval.LarkApproval.ApprovalNodes {
			if len(node.ApproveUsers) == 0 {
				return errors.Errorf("num of approval-node %d approver is 0", i)
			}
			if !lo.Contains([]string{"AND", "OR"}, string(node.Type)) {
				return errors.Errorf("approval-node %d type should be AND or OR", i)
			}
		}
	case config.DingTalkApproval:
		if approval.DingTalkApproval == nil {
			return errors.New("approval not found")
		}
		userIDSets := sets.NewString()
		if len(approval.DingTalkApproval.ApprovalNodes) > 20 {
			return errors.New("num of approval-node should not exceed 20")
		}
		if len(approval.DingTalkApproval.ApprovalNodes) == 0 {
			return errors.New("num of approval-node is 0")
		}
		for i, node := range approval.DingTalkApproval.ApprovalNodes {
			if len(node.ApproveUsers) == 0 {
				return errors.Errorf("num of approval-node %d approver is 0", i)
			}
			for _, user := range node.ApproveUsers {
				if userIDSets.Has(user.ID) {
					return errors.Errorf("Duplicate approvers %s should not appear in a complete approval process", user.Name)
				}
				userIDSets.Insert(user.ID)
			}
			if !lo.Contains([]string{"AND", "OR"}, string(node.Type)) {
				return errors.Errorf("approval-node %d type should be AND or OR", i)
			}
		}
	default:
		return errors.Errorf("invalid approval type %s", approval.Type)
	}

	return nil
}
