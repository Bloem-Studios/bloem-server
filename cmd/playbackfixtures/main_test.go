package main

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
