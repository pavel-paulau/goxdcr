package metadata

import (
	"encoding/json"
	"fmt"
)

const (
	//the maximum number of checkpoints ketp in the file
	MaxCheckpointsKept int = 100

	FailOverUUID        string = "failover_uuid"
	Seqno               string = "seqno"
	DcpSnapshotSeqno    string = "dcp_snapshot_seqno"
	DcpSnapshotEndSeqno string = "dcp_snapshot_end_seqno"
	TargetVbOpaque      string = "target_vb_opaque"
	TargetSeqno         string = "target_seqno"
	TargetVbUuid        string = "target_vb_uuid"
	StartUpTime         string = "startup_time"
)

type CheckpointRecord struct {
	//source vbucket failover uuid
	Failover_uuid uint64 `json:"failover_uuid"`
	//source vbucket high sequence number
	Seqno uint64 `json:"seqno"`
	//source snapshot start sequence number
	Dcp_snapshot_seqno uint64 `json:"dcp_snapshot_seqno"`
	//source snapshot end sequence number
	Dcp_snapshot_end_seqno uint64 `json:"dcp_snapshot_end_seqno"`
	//target vb opaque
	Target_vb_opaque TargetVBOpaque `json:"target_vb_opaque"`
	//target vb high sequence number
	Target_Seqno uint64 `json:"target_seqno"`
}

func (ckptRecord *CheckpointRecord) IsSame(new_record *CheckpointRecord) bool {
	if ckptRecord == nil && new_record != nil {
		return false
	} else if ckptRecord != nil && new_record == nil {
		return false
	} else if ckptRecord == nil && new_record == nil {
		return true
	} else if ckptRecord.Failover_uuid == new_record.Failover_uuid &&
		ckptRecord.Seqno == new_record.Seqno &&
		ckptRecord.Dcp_snapshot_seqno == new_record.Dcp_snapshot_seqno &&
		ckptRecord.Dcp_snapshot_end_seqno == new_record.Dcp_snapshot_end_seqno &&
		ckptRecord.Target_vb_opaque.IsSame(new_record.Target_vb_opaque) &&
		ckptRecord.Target_Seqno == new_record.Target_Seqno {
		return true
	} else {
		return false
	}
}
func (ckptRecord *CheckpointRecord) UnmarshalJSON(data []byte) error {
	var fieldMap map[string]interface{}
	err := json.Unmarshal(data, &fieldMap)
	if err != nil {
		return err
	}

	failover_uuid, ok := fieldMap[FailOverUUID]
	if ok {
		ckptRecord.Failover_uuid = uint64(failover_uuid.(float64))
	}

	seqno, ok := fieldMap[Seqno]
	if ok {
		ckptRecord.Seqno = uint64(seqno.(float64))
	}

	dcp_snapshot_seqno, ok := fieldMap[DcpSnapshotSeqno]
	if ok {
		ckptRecord.Dcp_snapshot_seqno = uint64(dcp_snapshot_seqno.(float64))
	}

	dcp_snapshot_end_seqno, ok := fieldMap[DcpSnapshotEndSeqno]
	if ok {
		ckptRecord.Dcp_snapshot_end_seqno = uint64(dcp_snapshot_end_seqno.(float64))
	}

	target_seqno, ok := fieldMap[TargetSeqno]
	if ok {
		ckptRecord.Target_Seqno = uint64(target_seqno.(float64))
	}

	// this is the special logic where we unmarshal targetVBOpaque into different concrete types
	target_vb_opaque, ok := fieldMap[TargetVbOpaque]
	if ok {
		target_vb_opaque_obj, err := UnmarshalTargetVBOpaque(target_vb_opaque)
		if err != nil {
			return err
		}
		ckptRecord.Target_vb_opaque = target_vb_opaque_obj
	}

	return nil
}

type TargetVBOpaque interface {
	Value() interface{}
	IsSame(targetVBOpaque TargetVBOpaque) bool
}

// older clusters have a single int vbuuid
type TargetVBUuid struct {
	Target_vb_uuid uint64 `json:"target_vb_uuid"`
}

func (targetVBUuid *TargetVBUuid) Value() interface{} {
	return targetVBUuid.Target_vb_uuid
}

func (targetVBUuid *TargetVBUuid) IsSame(targetVBOpaque TargetVBOpaque) bool {
	if targetVBUuid == nil && targetVBOpaque == nil {
		return true
	} else if targetVBUuid == nil && targetVBOpaque != nil {
		return false
	} else if targetVBUuid != nil && targetVBOpaque == nil {
		return false
	} else {
		new_targetVBUuid, ok := targetVBOpaque.(*TargetVBUuid)
		if !ok {
			return false
		} else {
			return targetVBUuid.Target_vb_uuid == new_targetVBUuid.Target_vb_uuid
		}
	}
}

// elasticSearch clusters have a single string vbuuid
type TargetVBUuidStr struct {
	Target_vb_uuid string `json:"target_vb_uuid"`
}

func (targetVBUuid *TargetVBUuidStr) Value() interface{} {
	return targetVBUuid.Target_vb_uuid
}

func (targetVBUuid *TargetVBUuidStr) IsSame(targetVBOpaque TargetVBOpaque) bool {
	if targetVBUuid == nil && targetVBOpaque == nil {
		return true
	} else if targetVBUuid == nil && targetVBOpaque != nil {
		return false
	} else if targetVBUuid != nil && targetVBOpaque == nil {
		return false
	} else {
		new_targetVBUuid, ok := targetVBOpaque.(*TargetVBUuidStr)
		if !ok {
			return false
		} else {
			return targetVBUuid.Target_vb_uuid == new_targetVBUuid.Target_vb_uuid
		}
	}
}

// newer clusters have a pair of vbuuid and seqno
type TargetVBUuidAndTimestamp struct {
	Target_vb_uuid string `json:"target_vb_uuid"`
	Startup_time   string `json:"startup_time"`
}

func (targetVBUuidAndTimestamp *TargetVBUuidAndTimestamp) Value() interface{} {
	valueArr := make([]interface{}, 2)
	valueArr[0] = targetVBUuidAndTimestamp.Target_vb_uuid
	valueArr[1] = targetVBUuidAndTimestamp.Startup_time
	return valueArr
}

func (targetVBUuidAndTimestamp *TargetVBUuidAndTimestamp) IsSame(targetVBOpaque TargetVBOpaque) bool {
	if targetVBUuidAndTimestamp == nil && targetVBOpaque == nil {
		return true
	} else if targetVBUuidAndTimestamp == nil && targetVBOpaque != nil {
		return false
	} else if targetVBUuidAndTimestamp != nil && targetVBOpaque == nil {
		return false
	} else {
		new_targetVBUuidAndTimestamp, ok := targetVBOpaque.(*TargetVBUuidAndTimestamp)
		if !ok {
			return false
		} else {
			return targetVBUuidAndTimestamp.Target_vb_uuid == new_targetVBUuidAndTimestamp.Target_vb_uuid && targetVBUuidAndTimestamp.Startup_time == new_targetVBUuidAndTimestamp.Startup_time
		}
	}
}

func UnmarshalTargetVBOpaque(data interface{}) (TargetVBOpaque, error) {
	if data == nil {
		return nil, nil
	}

	fieldMap, ok := data.(map[string]interface{})
	if !ok {
		return nil, TargetVBOpaqueUnmarshalError(data)
	}

	if len(fieldMap) == 1 {
		// unmarshal TargetVBUuid
		target_vb_uuid, ok := fieldMap[TargetVbUuid]
		if !ok {
			return nil, TargetVBOpaqueUnmarshalError(data)
		}

		target_vb_uuid_float, ok := target_vb_uuid.(float64)
		if ok {
			return &TargetVBUuid{uint64(target_vb_uuid_float)}, nil
		}

		target_vb_uuid_string, ok := target_vb_uuid.(string)
		if ok {
			return &TargetVBUuidStr{target_vb_uuid_string}, nil
		}

		return nil, TargetVBOpaqueUnmarshalError(data)

	} else if len(fieldMap) == 2 {
		// unmarshal TargetVBUuidAndTimestamp
		target_vb_uuid, ok := fieldMap[TargetVbUuid]
		if !ok {
			return nil, TargetVBOpaqueUnmarshalError(data)
		}

		target_vb_uuid_string, ok := target_vb_uuid.(string)
		if !ok {
			return nil, TargetVBOpaqueUnmarshalError(data)
		}

		startup_time, ok := fieldMap[StartUpTime]
		if !ok {
			return nil, TargetVBOpaqueUnmarshalError(data)
		}

		startup_time_string, ok := startup_time.(string)
		if !ok {
			return nil, TargetVBOpaqueUnmarshalError(data)
		}

		return &TargetVBUuidAndTimestamp{target_vb_uuid_string, startup_time_string}, nil
	}

	return nil, TargetVBOpaqueUnmarshalError(data)
}

func TargetVBOpaqueUnmarshalError(data interface{}) error {
	return fmt.Errorf("error unmarshaling target vb opaque. data=%v", data)
}

func (ckpt_record *CheckpointRecord) String() string {
	return fmt.Sprintf("{Failover_uuid=%v; Seqno=%v; Dcp_snapshot_seqno=%v; Dcp_snapshot_end_seqno=%v; Target_vb_opaque=%v; Commitopaque=%v}",
		ckpt_record.Failover_uuid, ckpt_record.Seqno, ckpt_record.Dcp_snapshot_seqno, ckpt_record.Dcp_snapshot_end_seqno, ckpt_record.Target_vb_opaque,
		ckpt_record.Target_Seqno)
}

type CheckpointsDoc struct {
	//keep 100 checkpoint record
	Checkpoint_records []*CheckpointRecord `json:"checkpoints"`

	//revision number
	Revision interface{}
}

func (ckpt *CheckpointRecord) ToMap() map[string]interface{} {
	ckpt_record_map := make(map[string]interface{})
	ckpt_record_map[FailOverUUID] = ckpt.Failover_uuid
	ckpt_record_map[Seqno] = ckpt.Seqno
	ckpt_record_map[DcpSnapshotSeqno] = ckpt.Dcp_snapshot_seqno
	ckpt_record_map[DcpSnapshotEndSeqno] = ckpt.Dcp_snapshot_end_seqno
	ckpt_record_map[TargetVbOpaque] = ckpt.Target_vb_opaque
	ckpt_record_map[TargetSeqno] = ckpt.Target_Seqno
	return ckpt_record_map
}

func NewCheckpointsDoc() *CheckpointsDoc {
	ckpt_doc := &CheckpointsDoc{Checkpoint_records: []*CheckpointRecord{},
		Revision: nil}

	for i := 0; i < MaxCheckpointsKept; i++ {
		ckpt_doc.Checkpoint_records = append(ckpt_doc.Checkpoint_records, nil)
	}

	return ckpt_doc
}

//Not currentcy safe. It should be used by one goroutine only
func (ckptsDoc *CheckpointsDoc) AddRecord(record *CheckpointRecord) bool {
	if len(ckptsDoc.Checkpoint_records) > 0 {
		if !ckptsDoc.Checkpoint_records[0].IsSame(record) {
			for i := len(ckptsDoc.Checkpoint_records) - 1; i >= 0; i-- {
				if i+1 < MaxCheckpointsKept {
					ckptsDoc.Checkpoint_records[i+1] = ckptsDoc.Checkpoint_records[i]
				}
			}
			ckptsDoc.Checkpoint_records[0] = record
			return true
		} else {
			return false
		}
	} else {
		ckptsDoc.Checkpoint_records = append(ckptsDoc.Checkpoint_records, record)
		return true
	}
}
