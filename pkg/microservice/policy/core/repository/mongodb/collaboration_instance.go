/*
Copyright 2023 The KodeRover Authors.

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

package mongodb

import (
	"context"

	"github.com/koderover/zadig/pkg/microservice/aslan/config"
	"github.com/koderover/zadig/pkg/microservice/user/core/repository/models"
	mongotool "github.com/koderover/zadig/pkg/tool/mongo"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
)

type CollaborationInstanceColl struct {
	*mongo.Collection

	coll string
}

func NewCollaborationInstanceColl() *CollaborationInstanceColl {
	name := models.CollaborationInstance{}.TableName()
	return &CollaborationInstanceColl{Collection: mongotool.Database(config.MongoDatabase()).Collection(name), coll: name}
}

func (c *CollaborationInstanceColl) GetCollectionName() string {
	return c.coll
}

func (c *CollaborationInstanceColl) FindInstance(uid, projectKey string) (*models.CollaborationInstance, error) {
	res := &models.CollaborationInstance{}

	query := bson.M{}
	query["project_name"] = projectKey
	query["user_uid"] = uid

	err := c.FindOne(context.TODO(), query).Decode(res)
	return res, err
}
