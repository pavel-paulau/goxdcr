// Copyright (c) 2013 Couchbase, Inc.
// Licensed under the Apache License, Version 2.0 (the "License"); you may not use this file
// except in compliance with the License. You may obtain a copy of the License at
//   http://www.apache.org/licenses/LICENSE-2.0
// Unless required by applicable law or agreed to in writing, software distributed under the
// License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND,
// either express or implied. See the License for the specific language governing permissions
// and limitations under the License.

package service_impl

import (
	"fmt"
	"github.com/couchbase/gomemcached"
	mcc "github.com/couchbase/gomemcached/client"
	common "github.com/couchbase/goxdcr/common"
	"github.com/couchbase/goxdcr/log"
	"github.com/couchbase/goxdcr/parts"
	"github.com/couchbase/goxdcr/pipeline_utils"
	"github.com/couchbase/goxdcr/utils"
	"sort"
	"sync"
)

type ThroughSeqnoTrackerSvc struct {
	// list of vbs that the tracker tracks
	vb_list []uint16

	// through_seqno seen by outnozzles based on the docs that are actually sent to target
	through_seqno_map       map[uint16]uint64
	through_seqno_map_locks map[uint16]*sync.RWMutex

	// stores for each vb a sorted list of the seqnos that have been sent to and confirmed by target
	vb_sent_seqno_list_map       map[uint16][]int
	vb_sent_seqno_list_map_locks map[uint16]*sync.RWMutex

	// Note: the following two lists are treated in the same way in through_seqno computation
	// they are maintained as two seperate lists because insertions into the lists are simpler
	// and quicker this way - each insertion is simply an append to the end of the list

	// stores for each vb a sorted list of seqnos that have been filtered out
	vb_filtered_seqno_list_map       map[uint16][]int
	vb_filtered_seqno_list_map_locks map[uint16]*sync.RWMutex
	// stores for each vb a sorted list of seqnos that have failed conflict resolution on source
	vb_failed_cr_seqno_list_map       map[uint16][]int
	vb_failed_cr_seqno_list_map_locks map[uint16]*sync.RWMutex

	// stores for each vb a sorted list of gap seqnos that have not been streamed out by dcp
	vb_gap_seqno_list_map       map[uint16][]int
	vb_gap_seqno_list_map_locks map[uint16]*sync.RWMutex

	// tracks the last seen seqno streamed out by dcp, so that we can tell the gap between the last seen seqno
	// and the current seen seqno
	vb_last_seen_seqno_map       map[uint16]uint64
	vb_last_seen_seqno_map_locks map[uint16]*sync.RWMutex

	topic string

	logger *log.CommonLogger
}

func NewThroughSeqnoTrackerSvc(logger_ctx *log.LoggerContext) *ThroughSeqnoTrackerSvc {
	logger := log.NewLogger("ThroughSeqnoTrackerSvc", logger_ctx)
	tsTracker := &ThroughSeqnoTrackerSvc{
		logger:                            logger,
		through_seqno_map:                 make(map[uint16]uint64),
		through_seqno_map_locks:           make(map[uint16]*sync.RWMutex),
		vb_sent_seqno_list_map:            make(map[uint16][]int),
		vb_sent_seqno_list_map_locks:      make(map[uint16]*sync.RWMutex),
		vb_filtered_seqno_list_map:        make(map[uint16][]int),
		vb_filtered_seqno_list_map_locks:  make(map[uint16]*sync.RWMutex),
		vb_failed_cr_seqno_list_map:       make(map[uint16][]int),
		vb_failed_cr_seqno_list_map_locks: make(map[uint16]*sync.RWMutex),
		vb_gap_seqno_list_map:             make(map[uint16][]int),
		vb_gap_seqno_list_map_locks:       make(map[uint16]*sync.RWMutex),
		vb_last_seen_seqno_map:            make(map[uint16]uint64),
		vb_last_seen_seqno_map_locks:      make(map[uint16]*sync.RWMutex)}
	return tsTracker
}

func (tsTracker *ThroughSeqnoTrackerSvc) initialize(pipeline common.Pipeline) {
	tsTracker.vb_list = pipeline_utils.GetSourceVBListPerPipeline(pipeline)
	for _, vbno := range tsTracker.vb_list {
		tsTracker.through_seqno_map[vbno] = 0
		tsTracker.through_seqno_map_locks[vbno] = &sync.RWMutex{}

		tsTracker.vb_sent_seqno_list_map[vbno] = make([]int, 0)
		tsTracker.vb_sent_seqno_list_map_locks[vbno] = &sync.RWMutex{}

		tsTracker.vb_filtered_seqno_list_map[vbno] = make([]int, 0)
		tsTracker.vb_filtered_seqno_list_map_locks[vbno] = &sync.RWMutex{}

		tsTracker.vb_failed_cr_seqno_list_map[vbno] = make([]int, 0)
		tsTracker.vb_failed_cr_seqno_list_map_locks[vbno] = &sync.RWMutex{}

		tsTracker.vb_gap_seqno_list_map[vbno] = make([]int, 0)
		tsTracker.vb_gap_seqno_list_map_locks[vbno] = &sync.RWMutex{}

		tsTracker.vb_last_seen_seqno_map[vbno] = 0
		tsTracker.vb_last_seen_seqno_map_locks[vbno] = &sync.RWMutex{}
	}
	tsTracker.topic = pipeline.Topic()
}

func (tsTracker *ThroughSeqnoTrackerSvc) Attach(pipeline common.Pipeline) error {
	tsTracker.logger.Infof("Attach through seqno tracker with pipeline %v\n", pipeline.InstanceId())

	tsTracker.initialize(pipeline)

	outNozzle_parts := pipeline.Targets()
	for _, part := range outNozzle_parts {
		part.RegisterComponentEventListener(common.DataSent, tsTracker)
		part.RegisterComponentEventListener(common.DataFailedCRSource, tsTracker)
	}

	dcp_parts := pipeline.Sources()
	for _, dcp := range dcp_parts {
		dcp.RegisterComponentEventListener(common.DataReceived, tsTracker)

		//get connector, which is a router
		router := dcp.Connector().(*parts.Router)
		router.RegisterComponentEventListener(common.DataFiltered, tsTracker)
	}

	return nil
}

func (tsTracker *ThroughSeqnoTrackerSvc) OnEvent(eventType common.ComponentEventType,
	item interface{},
	component common.Component,
	derivedItems []interface{},
	otherInfos map[string]interface{}) {
	if eventType == common.DataSent {
		vbno := item.(*gomemcached.MCRequest).VBucket
		seqno := otherInfos[parts.EVENT_ADDI_SEQNO].(uint64)
		tsTracker.addSentSeqno(vbno, seqno)
	} else if eventType == common.DataFiltered {
		seqno := item.(*mcc.UprEvent).Seqno
		vbno := item.(*mcc.UprEvent).VBucket
		tsTracker.addFilteredSeqno(vbno, seqno)
	} else if eventType == common.DataFailedCRSource {
		seqno, ok := otherInfos[parts.EVENT_ADDI_SEQNO].(uint64)
		if ok {
			vbno := item.(*gomemcached.MCRequest).VBucket
			tsTracker.addFailedCRSeqno(vbno, seqno)
		}
	} else if eventType == common.DataReceived {
		seqno := item.(*mcc.UprEvent).Seqno
		vbno := item.(*mcc.UprEvent).VBucket
		tsTracker.processGapSeqnos(vbno, seqno)
	} else {
		panic(fmt.Sprintf("Incorrect event type, %v, received by through_seqno service for pipeline %v", eventType, tsTracker.topic))
	}

}

func (tsTracker *ThroughSeqnoTrackerSvc) addSentSeqno(vbno uint16, sent_seqno uint64) {
	tsTracker.vb_sent_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_sent_seqno_list_map_locks[vbno].Unlock()

	sent_seqno_list := tsTracker.vb_sent_seqno_list_map[vbno]

	oldlen := len(sent_seqno_list)
	index, found := search(sent_seqno_list, sent_seqno)
	if found {
		panic(fmt.Sprintf("trying to add a duplicate seqno, %v, to sent seqno list, %v.", sent_seqno, sent_seqno_list))
	}

	newlist := []int{}
	newlist = append(newlist, sent_seqno_list[0:index]...)
	newlist = append(newlist, int(sent_seqno))
	if index < len(sent_seqno_list) {
		newlist = append(newlist, sent_seqno_list[index:]...)
	}
	newlen := len(newlist)
	tsTracker.vb_sent_seqno_list_map[vbno] = newlist
	if !sort.IntsAreSorted(tsTracker.vb_sent_seqno_list_map[vbno]) || newlen != oldlen+1 {
		panic(fmt.Sprintf("list %v is not valid. vbno=%v", tsTracker.vb_sent_seqno_list_map[vbno], vbno))
	}

	tsTracker.logger.Debugf("%v added sent seqno %v for vb %v. sent_seqno_list=%v\n", tsTracker.topic, sent_seqno, vbno, tsTracker.vb_filtered_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) addFilteredSeqno(vbno uint16, filtered_seqno uint64) {
	tsTracker.vb_filtered_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_filtered_seqno_list_map_locks[vbno].Unlock()
	tsTracker.vb_filtered_seqno_list_map[vbno] = append(tsTracker.vb_filtered_seqno_list_map[vbno], int(filtered_seqno))
	tsTracker.logger.Debugf("%v added filtered seqno %v for vb %v. filtered_seqno_list=%v\n", tsTracker.topic, filtered_seqno, vbno, tsTracker.vb_filtered_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) addFailedCRSeqno(vbno uint16, failed_cr_seqno uint64) {
	tsTracker.vb_failed_cr_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_failed_cr_seqno_list_map_locks[vbno].Unlock()
	tsTracker.vb_failed_cr_seqno_list_map[vbno] = append(tsTracker.vb_failed_cr_seqno_list_map[vbno], int(failed_cr_seqno))
	tsTracker.logger.Debugf("%v added failed cr seqno %v for vb %v. failed_cr_seqno_list=%v\n", tsTracker.topic, failed_cr_seqno, vbno, tsTracker.vb_failed_cr_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) getSentSeqnoList(vbno uint16) []int {
	tsTracker.vb_sent_seqno_list_map_locks[vbno].RLock()
	defer tsTracker.vb_sent_seqno_list_map_locks[vbno].RUnlock()
	return utils.DeepCopyIntArray(tsTracker.vb_sent_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) getFilteredSeqnoList(vbno uint16) []int {
	tsTracker.vb_filtered_seqno_list_map_locks[vbno].RLock()
	defer tsTracker.vb_filtered_seqno_list_map_locks[vbno].RUnlock()
	return utils.DeepCopyIntArray(tsTracker.vb_filtered_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) getFailedCRSeqnoList(vbno uint16) []int {
	tsTracker.vb_failed_cr_seqno_list_map_locks[vbno].RLock()
	defer tsTracker.vb_failed_cr_seqno_list_map_locks[vbno].RUnlock()
	return utils.DeepCopyIntArray(tsTracker.vb_failed_cr_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) getGapSeqnoList(vbno uint16) []int {
	tsTracker.vb_gap_seqno_list_map_locks[vbno].RLock()
	defer tsTracker.vb_gap_seqno_list_map_locks[vbno].RUnlock()
	return utils.DeepCopyIntArray(tsTracker.vb_gap_seqno_list_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) truncateSeqnoLists(vbno uint16, through_seqno uint64) {
	tsTracker.truncateSentSeqnoList(vbno, through_seqno)
	tsTracker.truncateFilteredSeqnoList(vbno, through_seqno)
	tsTracker.truncateFailedCRSeqnoList(vbno, through_seqno)
	tsTracker.truncateGapSeqnoList(vbno, through_seqno)
}

func (tsTracker *ThroughSeqnoTrackerSvc) truncateSentSeqnoList(vbno uint16, through_seqno uint64) {
	tsTracker.vb_sent_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_sent_seqno_list_map_locks[vbno].Unlock()
	sent_seqno_list := tsTracker.vb_sent_seqno_list_map[vbno]
	index, found := search(sent_seqno_list, through_seqno)
	if found {
		tsTracker.vb_sent_seqno_list_map[vbno] = sent_seqno_list[index+1:]
	} else if index > 0 {
		tsTracker.vb_sent_seqno_list_map[vbno] = sent_seqno_list[index:]
	}
}

func (tsTracker *ThroughSeqnoTrackerSvc) truncateFilteredSeqnoList(vbno uint16, through_seqno uint64) {
	tsTracker.vb_filtered_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_filtered_seqno_list_map_locks[vbno].Unlock()
	filtered_seqno_list := tsTracker.vb_filtered_seqno_list_map[vbno]
	index, found := search(filtered_seqno_list, through_seqno)
	if found {
		tsTracker.vb_filtered_seqno_list_map[vbno] = filtered_seqno_list[index+1:]
	} else if index > 0 {
		tsTracker.vb_filtered_seqno_list_map[vbno] = filtered_seqno_list[index:]
	}
}

func (tsTracker *ThroughSeqnoTrackerSvc) truncateFailedCRSeqnoList(vbno uint16, through_seqno uint64) {
	tsTracker.vb_failed_cr_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_failed_cr_seqno_list_map_locks[vbno].Unlock()
	failed_cr_seqno_list := tsTracker.vb_failed_cr_seqno_list_map[vbno]
	index, found := search(failed_cr_seqno_list, through_seqno)
	if found {
		tsTracker.vb_failed_cr_seqno_list_map[vbno] = failed_cr_seqno_list[index+1:]
	} else if index > 0 {
		tsTracker.vb_failed_cr_seqno_list_map[vbno] = failed_cr_seqno_list[index:]
	}
}

func (tsTracker *ThroughSeqnoTrackerSvc) truncateGapSeqnoList(vbno uint16, through_seqno uint64) {
	tsTracker.vb_gap_seqno_list_map_locks[vbno].Lock()
	defer tsTracker.vb_gap_seqno_list_map_locks[vbno].Unlock()
	gap_seqno_list := tsTracker.vb_gap_seqno_list_map[vbno]
	index, found := search(gap_seqno_list, through_seqno)
	if found {
		panic("through_seqno should not be in gap_seqno_list")
	} else if index > 0 {
		tsTracker.vb_gap_seqno_list_map[vbno] = gap_seqno_list[index:]
	}
}

func (tsTracker *ThroughSeqnoTrackerSvc) getCurrentThroughSeqno(vbno uint16) uint64 {
	tsTracker.through_seqno_map_locks[vbno].RLock()
	defer tsTracker.through_seqno_map_locks[vbno].RUnlock()
	return tsTracker.through_seqno_map[vbno]
}

func (tsTracker *ThroughSeqnoTrackerSvc) processGapSeqnos(vbno uint16, current_seqno uint64) {
	tsTracker.vb_last_seen_seqno_map_locks[vbno].Lock()
	defer tsTracker.vb_last_seen_seqno_map_locks[vbno].Unlock()
	last_seen_seqno := tsTracker.vb_last_seen_seqno_map[vbno]
	if last_seen_seqno == 0 {
		// this covers the case where the replication resumes from checkpoint docs
		last_seen_seqno = tsTracker.getCurrentThroughSeqno(vbno)
	}
	tsTracker.vb_last_seen_seqno_map[vbno] = current_seqno

	if last_seen_seqno < current_seqno-1 {
		tsTracker.vb_gap_seqno_list_map_locks[vbno].Lock()
		defer tsTracker.vb_gap_seqno_list_map_locks[vbno].Unlock()
		for i := last_seen_seqno + 1; i < current_seqno; i++ {
			tsTracker.vb_gap_seqno_list_map[vbno] = append(tsTracker.vb_gap_seqno_list_map[vbno], int(i))
		}
	}

	tsTracker.logger.Debugf("%v tsTracker.vb_last_seen_seqno_map[%v]=%v\n", tsTracker.topic, vbno, tsTracker.vb_last_seen_seqno_map[vbno])
}

func (tsTracker *ThroughSeqnoTrackerSvc) GetThroughSeqno(vbno uint16) uint64 {
	// lock through_seqno_map[vbno] throughout the computation to ensure that
	// two GetThroughSeqno() routines won't interleave, which would cause issues
	// since we truncate seqno lists in accordance with through_seqno
	tsTracker.through_seqno_map_locks[vbno].Lock()
	defer tsTracker.through_seqno_map_locks[vbno].Unlock()

	last_through_seqno := tsTracker.through_seqno_map[vbno]
	sent_seqno_list := tsTracker.getSentSeqnoList(vbno)
	max_sent_seqno := maxSeqno(sent_seqno_list)
	filtered_seqno_list := tsTracker.getFilteredSeqnoList(vbno)
	max_filtered_seqno := maxSeqno(filtered_seqno_list)
	failed_cr_seqno_list := tsTracker.getFailedCRSeqnoList(vbno)
	max_failed_cr_seqno := maxSeqno(failed_cr_seqno_list)
	gap_seqno_list := tsTracker.getGapSeqnoList(vbno)
	max_gap_seqno := maxSeqno(gap_seqno_list)

	tsTracker.logger.Debugf("%v, vbno=%v, last_through_seqno=%v\n sent_seqno_list=%v\n filtered_seqno_list=%v\n failed_cr_seqno_list=%v\n gap_seqno_list=%v\n", tsTracker.topic, vbno, last_through_seqno, sent_seqno_list, filtered_seqno_list, failed_cr_seqno_list, gap_seqno_list)

	// Goal of algorithm:
	// Find the right through_seqno for stats and checkpointing, with the constraint that through_seqno cannot be
	// in gap_seqno_list, since we do not want to use seqnos in gap_seqno_list for checkpointing

	// Starting from last_through_seqno, find the largest N such that last_through_seqno+1, last_through_seqno+2,
	// .., last_through_seqno+N all exist in filtered_seqno_list, failed_cr_seqno_list, sent_seqno_list, or gap_seqno_list,
	// and that last_through_seqno+N is not in gap_seqno_list
	// return last_through_seqno+N as the current through_seqno. Note that N could be 0.

	through_seqno := last_through_seqno

	iter_seqno := last_through_seqno
	var last_sent_index int = -1
	var last_filtered_index int = -1
	var last_failed_cr_index int = -1
	var found_seqno_type int = -1

	const (
		SeqnoTypeSent     int = 1
		SeqnoTypeFiltered int = 2
		SeqnoTypeFailedCR int = 3
	)

	for {
		iter_seqno = iter_seqno + 1
		if iter_seqno <= max_sent_seqno {
			sent_index, sent_found := search(sent_seqno_list, iter_seqno)
			if sent_found {
				last_sent_index = sent_index
				found_seqno_type = SeqnoTypeSent
				continue
			}
		}

		if iter_seqno <= max_filtered_seqno {
			filtered_index, filtered_found := search(filtered_seqno_list, iter_seqno)
			if filtered_found {
				last_filtered_index = filtered_index
				found_seqno_type = SeqnoTypeFiltered
				continue
			}
		}

		if iter_seqno <= max_failed_cr_seqno {
			failed_cr_index, failed_cr_found := search(failed_cr_seqno_list, iter_seqno)
			if failed_cr_found {
				last_failed_cr_index = failed_cr_index
				found_seqno_type = SeqnoTypeFailedCR
				continue
			}
		}

		if iter_seqno <= max_gap_seqno {
			_, gap_found := search(gap_seqno_list, iter_seqno)
			if gap_found {
				continue
			}
		}

		// stop if cannot find seqno in any of the lists
		break
	}

	if last_sent_index >= 0 || last_filtered_index >= 0 || last_failed_cr_index >= 0 {
		if found_seqno_type == SeqnoTypeSent {
			through_seqno = uint64(sent_seqno_list[last_sent_index])
		} else if found_seqno_type == SeqnoTypeFiltered {
			through_seqno = uint64(filtered_seqno_list[last_filtered_index])
		} else if found_seqno_type == SeqnoTypeFailedCR {
			through_seqno = uint64(failed_cr_seqno_list[last_failed_cr_index])
		} else {
			panic(fmt.Sprintf("unexpected found_seqno_type, %v", found_seqno_type))
		}

		tsTracker.through_seqno_map[vbno] = through_seqno

		// truncate no longer needed entries from seqno lists to reduce memory/cpu overhead for future computations
		go tsTracker.truncateSeqnoLists(vbno, through_seqno)
	}

	tsTracker.logger.Debugf("%v, vbno=%v, through_seqno=%v\n", tsTracker.topic, vbno, through_seqno)
	return through_seqno
}

func search(seqno_list []int, seqno uint64) (int, bool) {
	index := sort.Search(len(seqno_list), func(i int) bool {
		return seqno_list[i] >= int(seqno)
	})
	if index < len(seqno_list) && seqno_list[index] == int(seqno) {
		return index, true
	} else {
		return index, false
	}
}

func (tsTracker *ThroughSeqnoTrackerSvc) GetThroughSeqnos() map[uint16]uint64 {
	result_map := make(map[uint16]uint64)

	listOfVbs := tsTracker.vb_list
	vb_per_worker := 20
	start_index := 0

	wait_grp := &sync.WaitGroup{}
	executor_id := 0
	result_map_map := make(map[int]map[uint16]uint64)
	for {
		end_index := start_index + vb_per_worker
		if end_index > len(listOfVbs) {
			end_index = len(listOfVbs)
		}
		vbs_for_executor := listOfVbs[start_index:end_index]
		result_map_map[executor_id] = make(map[uint16]uint64)
		wait_grp.Add(1)
		go tsTracker.getThroughSeqnos(executor_id, vbs_for_executor, result_map_map[executor_id], wait_grp)
		start_index = end_index
		executor_id++
		if start_index >= len(listOfVbs) {
			break
		}
	}

	wait_grp.Wait()

	for _, exec_result_map := range result_map_map {
		for vbno, seqno := range exec_result_map {
			result_map[vbno] = seqno
		}
	}

	return result_map
}

func (tsTracker *ThroughSeqnoTrackerSvc) getThroughSeqnos(executor_id int, listOfVbs []uint16, result_map map[uint16]uint64, wait_grp *sync.WaitGroup) {
	if result_map == nil {
		panic("through_seqno_map is nil")
	}
	tsTracker.logger.Debugf("%v getThroughSeqnos executor %v is working on vbuckets %v", tsTracker.topic, executor_id, listOfVbs)
	if wait_grp == nil {
		panic("wait_grp can't be nil")
	}
	defer wait_grp.Done()

	for _, vbno := range listOfVbs {
		result_map[vbno] = tsTracker.GetThroughSeqno(vbno)
	}
}

func (tsTracker *ThroughSeqnoTrackerSvc) SetStartSeqnos(start_seqno_map map[uint16]uint64) {
	for vbno, _ := range start_seqno_map {
		tsTracker.setStartSeqno(vbno, start_seqno_map[vbno])
	}

	tsTracker.logger.Infof("%v through_seqno_map in through seqno tracker has been set to %v\n", tsTracker.topic, tsTracker.through_seqno_map)
}

func (tsTracker *ThroughSeqnoTrackerSvc) setStartSeqno(vbno uint16, seqno uint64) {
	tsTracker.through_seqno_map_locks[vbno].Lock()
	defer tsTracker.through_seqno_map_locks[vbno].Unlock()

	tsTracker.through_seqno_map[vbno] = seqno
}

func maxSeqno(seqno_list []int) uint64 {
	length := len(seqno_list)
	if length > 0 {
		return uint64(seqno_list[length-1])
	} else {
		return 0
	}
}
