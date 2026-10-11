package tasks

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

type bookTaskFixture struct {
	audioErr, ebookErr error
	ebookCalls         int
}

func (f *bookTaskFixture) Reconcile(context.Context, int) (int, error) { return 0, f.audioErr }
func (f *bookTaskFixture) ReconcileEbooks(context.Context, int) (int, error) {
	f.ebookCalls++
	return 0, f.ebookErr
}

type bookTaskProgress struct{}

func (bookTaskProgress) Report(float64, string)        {}
func (bookTaskProgress) SetResultData(json.RawMessage) {}

func TestBookRequestTaskEbookPassSurvivesAudiobookFailure(t *testing.T) {
	f := &bookTaskFixture{audioErr: errors.New("audiobook notice retry"), ebookErr: errors.New("ebook provider retry")}
	task := &BookRequestsTask{service: f}
	err := task.Execute(t.Context(), bookTaskProgress{})
	if f.ebookCalls != 1 || !errors.Is(err, f.audioErr) || !errors.Is(err, f.ebookErr) {
		t.Fatalf("one lane starved the other: calls=%d err=%v", f.ebookCalls, err)
	}
}
