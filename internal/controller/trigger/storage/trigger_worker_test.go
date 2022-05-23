// Copyright 2022 Linkall Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/linkall-labs/vanus/internal/controller/trigger/info"
	"github.com/linkall-labs/vanus/internal/kv"

	"github.com/golang/mock/gomock"
	. "github.com/smartystreets/goconvey/convey"
)

func TestSaveTriggerWorker(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	kvClient := kv.NewMockClient(ctrl)
	s := NewTriggerWorkerStorage(kvClient).(*triggerWorkerStorage)
	ID := "testID"
	Convey("create trigger worker", t, func() {
		kvClient.EXPECT().Exists(ctx, s.getKey(ID)).Return(false, nil)
		kvClient.EXPECT().Create(ctx, s.getKey(ID), gomock.Any()).Return(nil)
		err := s.SaveTriggerWorker(ctx, info.TriggerWorkerInfo{
			ID:   ID,
			Addr: "test",
		})
		So(err, ShouldBeNil)
	})

	Convey("update trigger worker", t, func() {
		kvClient.EXPECT().Exists(ctx, s.getKey(ID)).Return(true, nil)
		kvClient.EXPECT().Update(ctx, s.getKey(ID), gomock.Any()).Return(nil)
		err := s.SaveTriggerWorker(ctx, info.TriggerWorkerInfo{
			ID:   ID,
			Addr: "test",
		})
		So(err, ShouldBeNil)
	})
}

func TestGetTriggerWorker(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	kvClient := kv.NewMockClient(ctrl)
	s := NewTriggerWorkerStorage(kvClient).(*triggerWorkerStorage)
	ID := "testID"
	Convey("get trigger worker", t, func() {
		expect := info.TriggerWorkerInfo{
			ID:   ID,
			Addr: "test",
		}
		v, _ := json.Marshal(expect)
		kvClient.EXPECT().Get(ctx, s.getKey(ID)).Return(v, nil)
		data, err := s.GetTriggerWorker(ctx, ID)
		So(err, ShouldBeNil)
		So(data.Addr, ShouldEqual, expect.Addr)
	})
}

func TestDeleteTriggerWorker(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	kvClient := kv.NewMockClient(ctrl)
	s := NewTriggerWorkerStorage(kvClient).(*triggerWorkerStorage)
	ID := "testID"
	Convey("delete trigger worker", t, func() {
		kvClient.EXPECT().Delete(ctx, s.getKey(ID)).Return(nil)
		err := s.DeleteTriggerWorker(ctx, ID)
		So(err, ShouldBeNil)
	})
}

func TestListTriggerWorker(t *testing.T) {
	ctx := context.Background()
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	kvClient := kv.NewMockClient(ctrl)
	s := NewTriggerWorkerStorage(kvClient).(*triggerWorkerStorage)
	ID := "testID"
	Convey("list trigger worker", t, func() {
		expect := info.TriggerWorkerInfo{
			ID:   ID,
			Addr: "test",
		}
		v, _ := json.Marshal(expect)
		kvClient.EXPECT().List(ctx, s.getKey("/")).Return([]kv.Pair{
			{Key: fmt.Sprintf("%s", ID), Value: v},
		}, nil)
		list, err := s.ListTriggerWorker(ctx)
		So(err, ShouldBeNil)
		So(len(list), ShouldEqual, 1)
		So(list[0].Addr, ShouldEqual, expect.Addr)
	})
}