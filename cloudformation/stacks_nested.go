package cloudformation

// The stack half of nested stacks: the template fetcher a deploy uses, the
// child records a deploy writes, and the delete that takes them together.

import (
	"fmt"
	"sort"
)

// fetcher reads a nested stack's TemplateURL from the local S3, or from the
// bodies a record stored at deploy so a delete does not need the staging
// bucket to still hold them.
func (s *Server) fetcher(stored map[string]string) func(string) ([]byte, error) {
	return func(url string) ([]byte, error) {
		if body, ok := stored[url]; ok {
			return []byte(body), nil
		}
		bucket, key, err := parseS3URL(url)
		if err != nil {
			return nil, err
		}
		body, err := s.fetchS3(bucket, key)
		if err != nil {
			return nil, err
		}
		return []byte(body), nil
	}
}

// recordNested writes one StackRecord per child, owned by the parent, and
// keeps the child bodies on the parent for delete. The parent's own resource
// list already names each child as an AWS::CloudFormation::Stack.
func (s *Server) recordNested(parent *StackRecord, rep *Report, isUpdate bool) error {
	parent.NestedTemplates = map[string]string{}
	root := parent.RootID
	if root == "" {
		root = parent.ID
	}
	var walk func(owner *StackRecord, children []*NestedStack) error
	walk = func(owner *StackRecord, children []*NestedStack) error {
		for _, child := range children {
			parent.NestedTemplates[child.TemplateURL] = child.TemplateBody
			now := s.now().Unix()
			rec := &StackRecord{
				Name: child.Name, TemplateBody: child.TemplateBody, Parameters: child.Parameters,
				Created: now, Updated: now, ParentID: owner.ID, RootID: root,
				Status: parent.Status, StatusReason: parent.StatusReason,
			}
			if prev, _ := s.store.GetStack(child.Name); prev != nil {
				rec.ID, rec.Created = prev.ID, prev.Created
			} else {
				rec.ID = StackARN(child.Name, s.store.newID())
			}
			for _, e := range child.Report.Entries {
				if e.Kind != Mapped {
					continue
				}
				rec.Resources = append(rec.Resources, StackResource{
					LogicalID: e.LogicalID, Type: e.Type, PhysicalID: e.Name,
					Status: statusVerb(isUpdate) + "_COMPLETE", Props: e.Props,
				})
			}
			for k, v := range child.Report.Outputs {
				rec.Outputs = append(rec.Outputs, StackOutput{Key: k, Value: v})
			}
			sort.Slice(rec.Outputs, func(i, j int) bool { return rec.Outputs[i].Key < rec.Outputs[j].Key })
			rec.Events = []StackEvent{{
				ID: s.store.newID(), Timestamp: now, LogicalID: child.Name,
				Type: "AWS::CloudFormation::Stack", PhysicalID: rec.ID, Status: rec.Status,
			}}
			if err := s.store.PutStack(rec); err != nil {
				return err
			}
			// The parent's row for the child carries the child's stack id.
			for i := range parent.Resources {
				if parent.Resources[i].LogicalID == child.LogicalID {
					parent.Resources[i].PhysicalID = rec.ID
				}
			}
			if err := walk(rec, child.Report.Nested); err != nil {
				return err
			}
		}
		return nil
	}
	return walk(parent, rep.Nested)
}

// deleteNested marks every child of a deleted stack DELETE_COMPLETE; the
// resources went with the parent's Destroy, which ran on the merged graph.
func (s *Server) deleteNested(parent *StackRecord) {
	stacks, _ := s.store.ListStacks()
	for i := range stacks {
		st := &stacks[i]
		if st.ParentID != parent.ID || st.Status == StatusDeleteComplete {
			continue
		}
		s.deleteNested(st)
		now := s.now().Unix()
		st.Status, st.StatusReason, st.Updated = StatusDeleteComplete, "", now
		st.Resources, st.Outputs = nil, nil
		st.Events = append(st.Events, StackEvent{
			ID: s.store.newID(), Timestamp: now, LogicalID: st.Name,
			Type: "AWS::CloudFormation::Stack", PhysicalID: st.ID, Status: StatusDeleteComplete,
		})
		_ = s.store.PutStack(st)
	}
}

// refuseChildDelete is the check DeleteStack runs first: a nested stack goes
// with its parent, never on its own — deleting it alone would leave the
// parent describing resources that no longer exist.
func refuseChildDelete(st *StackRecord) error {
	if st.ParentID == "" || st.Status == StatusDeleteComplete {
		return nil
	}
	return fmt.Errorf("Stack [%s] is a nested stack of %s and cannot be deleted directly; delete the parent stack", st.Name, st.ParentID)
}
