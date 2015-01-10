// Copyright (c) 2013 Couchbase, Inc.
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
// except in compliance with the License. You may obtain a copy of the License at
//   http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed under the
// License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions
// and limitations under the License.

package service_def

import (
	"github.com/couchbase/goxdcr/metadata"
	"sync"
)

// Callback function for spec changed event
type SpecChangedCallback func(changedSpecId string, changedSpec *metadata.ReplicationSpecification) error

type ReplicationSpecSvc interface {
	ReplicationSpec(replicationId string) (*metadata.ReplicationSpecification, error)
	AddReplicationSpec(spec *metadata.ReplicationSpecification) error
	SetReplicationSpec(spec *metadata.ReplicationSpecification) error
	DelReplicationSpec(replicationId string) error
	ActiveReplicationSpecs() (map[string]*metadata.ReplicationSpecification, error)
	ActiveReplicationSpecIdsForBucket(bucket string) ([]string, error)

	// Register call back function for spec changed event
	StartSpecChangedCallBack(callBack SpecChangedCallback, cancel <-chan struct{}, waitGrp *sync.WaitGroup) error
}
