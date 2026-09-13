package main

// Bloem-owned tests for this package. Kept out of Silo's own test files so
// upstream merges do not conflict here; see contracts/seams.txt.

import (
	"slices"
	"testing"
)

func TestGoldenDecisionResponsePublishesNegotiatedClientFeatures(t *testing.T) {
	request := goldenStartRequest()
	response := goldenDecisionResponse()

	if !slices.Equal(response.NegotiatedClientFeatures, request.ClientFeatures) {
		t.Fatalf("negotiated client features = %v, want %v", response.NegotiatedClientFeatures, request.ClientFeatures)
	}
}
