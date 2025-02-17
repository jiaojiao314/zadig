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

package mongodb

import (
	"context"
	"fmt"

	"github.com/pkg/errors"
	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/bson/primitive"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"

	"github.com/koderover/zadig/pkg/microservice/aslan/config"
	"github.com/koderover/zadig/pkg/microservice/aslan/core/common/repository/models"
	mongotool "github.com/koderover/zadig/pkg/tool/mongo"
)

type ReleasePlanColl struct {
	*mongo.Collection

	coll string
}

func NewReleasePlanColl() *ReleasePlanColl {
	name := models.ReleasePlan{}.TableName()
	return &ReleasePlanColl{
		Collection: mongotool.Database(config.MongoDatabase()).Collection(name),
		coll:       name,
	}
}

func (c *ReleasePlanColl) GetCollectionName() string {
	return c.coll
}

func (c *ReleasePlanColl) EnsureIndex(ctx context.Context) error {
	return nil
}

func (c *ReleasePlanColl) Create(args *models.ReleasePlan) error {
	if args == nil {
		return errors.New("nil ReleasePlan")
	}

	_, err := c.InsertOne(context.Background(), args)
	return err
}

func (c *ReleasePlanColl) GetByID(ctx context.Context, idString string) (*models.ReleasePlan, error) {
	id, err := primitive.ObjectIDFromHex(idString)
	if err != nil {
		return nil, err
	}

	query := bson.M{"_id": id}
	result := new(models.ReleasePlan)
	err = c.FindOne(ctx, query).Decode(result)
	return result, err
}

func (c *ReleasePlanColl) UpdateByID(ctx context.Context, idString string, args *models.ReleasePlan) error {
	if args == nil {
		return errors.New("nil ReleasePlan")
	}
	id, err := primitive.ObjectIDFromHex(idString)
	if err != nil {
		return fmt.Errorf("invalid id")
	}

	query := bson.M{"_id": id}
	change := bson.M{"$set": args}
	_, err = c.UpdateOne(ctx, query, change)
	return err
}

func (c *ReleasePlanColl) DeleteByID(ctx context.Context, idString string) error {
	id, err := primitive.ObjectIDFromHex(idString)
	if err != nil {
		return err
	}

	query := bson.M{"_id": id}
	_, err = c.DeleteOne(ctx, query)
	return err
}

type ListReleasePlanOption struct {
	PageNum        int64
	PageSize       int64
	IsSort         bool
	ExcludedFields []string
	Status         config.ReleasePlanStatus
}

func (c *ReleasePlanColl) ListByOptions(opt *ListReleasePlanOption) ([]*models.ReleasePlan, int64, error) {
	if opt == nil {
		return nil, 0, errors.New("nil ListOption")
	}

	query := bson.M{}

	var resp []*models.ReleasePlan
	ctx := context.Background()
	opts := options.Find()
	if opt.IsSort {
		opts.SetSort(bson.D{{"index", -1}})
	}
	if opt.PageNum > 0 && opt.PageSize > 0 {
		opts.SetSkip((opt.PageNum - 1) * opt.PageSize)
		opts.SetLimit(opt.PageSize)
	}
	if opt.Status != "" {
		query["status"] = opt.Status
	}
	if len(opt.ExcludedFields) > 0 {
		projection := bson.M{}
		for _, field := range opt.ExcludedFields {
			projection[field] = 0
		}
		opts.SetProjection(projection)
	}

	count, err := c.Collection.CountDocuments(ctx, query)
	if err != nil {
		return nil, 0, err
	}
	cursor, err := c.Collection.Find(ctx, query, opts)
	if err != nil {
		return nil, 0, err
	}

	err = cursor.All(ctx, &resp)
	if err != nil {
		return nil, 0, err
	}

	return resp, count, nil
}
