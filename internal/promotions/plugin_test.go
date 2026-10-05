package promotions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Silo-Server/silo-server/internal/bloempresentation"
)

func TestPromotionWorkerProcess(t *testing.T) {
	if len(os.Args) == 0 || os.Args[len(os.Args)-1] != "promotions-test-worker" {
		return
	}
	if err := RunPlugin(os.Stdin, os.Stdout); err != nil {
		os.Exit(8)
	}
	os.Exit(0)
}

func promotionTestClient(t *testing.T) *bloempresentation.Client {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	wrapper := filepath.Join(t.TempDir(), "promotions-worker")
	quoted := "'" + strings.ReplaceAll(executable, "'", "'\\''") + "'"
	if err := os.WriteFile(wrapper, []byte("#!/bin/sh\nexec "+quoted+" -test.run=^TestPromotionWorkerProcess$ promotions-test-worker\n"), 0700); err != nil {
		t.Fatal(err)
	}
	client, err := bloempresentation.New(wrapper)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Error(err)
		}
	})
	return client
}

func TestPluginProcessMatchesEngine(t *testing.T) {
	client := promotionTestClient(t)
	evaluator := NewPluginEvaluator(client)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	p := enginePromotion("campaign")
	p.Placement.PlaybackStyle = "card"
	pos := 4
	p.Placement.HomePosition = &pos
	candidateInput := CandidateInput{Promotions: []Promotion{p}, Query: Query{Surface: SurfaceHome}, Now: promoStart}
	want, err := (Engine{}).Candidates(ctx, candidateInput)
	if err != nil {
		t.Fatal(err)
	}
	got, err := evaluator.Candidates(ctx, candidateInput)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("process candidates=%+v want=%+v err=%v", got, want, err)
	}
	// JSON must preserve unrestricted nil and restricted empty library slices.
	p.Targeting = Targeting{Audience: "library", LibraryID: 8}
	for _, libraries := range [][]int{nil, {}, {8}, {9}} {
		in := DeliveryInput{Promotions: []Promotion{p}, Viewer: Viewer{LibraryIDs: libraries}}
		want, err := (Engine{}).Deliver(ctx, in)
		if err != nil {
			t.Fatal(err)
		}
		got, err := evaluator.Deliver(ctx, in)
		if err != nil || !reflect.DeepEqual(got, want) {
			t.Fatalf("libraries=%v process=%+v want=%+v err=%v", libraries, got, want, err)
		}
	}
}

func TestRunPluginRejectsUnknownOperationAndMalformedPayload(t *testing.T) {
	for _, request := range []string{
		`{"version":1,"operation":"unknown","payload":{}}` + "\n",
		`{"version":1,"operation":"promotions.candidates","payload":{"now":42}}` + "\n",
		`{"version":1,"operation":"promotions.deliver","payload":{"viewer":42}}` + "\n",
	} {
		var out bytes.Buffer
		if err := RunPlugin(strings.NewReader(request), &out); err != nil {
			t.Fatal(err)
		}
		var response struct {
			Error string `json:"error"`
		}
		if err := json.Unmarshal(out.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
		if response.Error == "" {
			t.Fatalf("accepted invalid request: %s", out.Bytes())
		}
	}
}

func TestPluginEvaluatorEnforcesHostTargetingAndCandidateBounds(t *testing.T) {
	p := enginePromotion("restricted")
	p.Targeting = Targeting{Audience: "library", LibraryID: 8}
	evaluator := NewPluginEvaluator(promotionCaller(func(_ context.Context, op string, _ any, out any) error {
		switch op {
		case OperationCandidates:
			out.(*CandidateResult).IDs = []string{p.ID}
		case OperationDeliver:
			r := out.(*DeliveryResult)
			r.Cards = []Card{p.Card()}
			r.HomePosition = DefaultHomePosition
		}
		return nil
	}))
	if _, err := evaluator.Deliver(context.Background(), DeliveryInput{Promotions: []Promotion{p}, Viewer: Viewer{LibraryIDs: []int{9}}}); !errors.Is(err, ErrPluginResponse) {
		t.Fatalf("widened targeting: %v", err)
	}
	for _, input := range []CandidateInput{
		{Promotions: []Promotion{p}, Query: Query{Surface: SurfaceHome}, Now: promoEnd},
		{Promotions: []Promotion{p}, Query: Query{Surface: SurfaceHome}, Now: promoStart, DismissedIDs: []string{p.ID}},
		{Promotions: []Promotion{p}, Query: Query{Surface: SurfaceDetail}, Now: promoStart, DismissedIDs: []string{p.ID}},
	} {
		if _, err := evaluator.Candidates(context.Background(), input); !errors.Is(err, ErrPluginResponse) {
			t.Fatalf("widened candidate bounds: %v", err)
		}
	}
}

func TestPluginPayloadOmitsHostRepositoryMetadata(t *testing.T) {
	p := enginePromotion("campaign")
	p.CreatedBy = 8123
	p.Priority = 17
	for _, input := range []any{
		CandidateInput{Promotions: []Promotion{p}, Query: Query{Surface: SurfaceHome, Viewer: Viewer{UserID: 8123}}, Now: promoStart},
		DeliveryInput{Promotions: []Promotion{p}, Viewer: Viewer{UserID: 7}},
	} {
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		for _, hostField := range []string{"organization_id", "created_by", "created_at", "updated_at", "image_width", "image_height", "priority"} {
			if bytes.Contains(raw, []byte(hostField)) {
				t.Fatalf("host metadata crossed plugin boundary: %s", raw)
			}
		}
	}
}
