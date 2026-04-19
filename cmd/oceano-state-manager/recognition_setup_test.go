package main

import "testing"

func TestNewRecognitionComponents_EmptyPlan(t *testing.T) {
	components := newRecognitionComponents(RecognitionPlan{})
	if components.chain != nil {
		t.Fatal("expected nil chain for empty plan")
	}
	if components.confirmer != nil {
		t.Fatal("expected nil confirmer for empty plan")
	}
	if components.continuity != nil {
		t.Fatal("expected nil continuity recognizer for empty plan")
	}
}

func TestNewRecognitionComponents_PreservesOrder(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	components := newRecognitionComponents(RecognitionPlan{Ordered: []Recognizer{a, b}})
	chain, ok := components.chain.(*ChainRecognizer)
	if !ok {
		t.Fatal("expected chain recognizer")
	}
	if chain.Name() != "A→B" {
		t.Fatalf("unexpected chain order: %s", chain.Name())
	}
}

func TestNewRecognitionComponents_RolesIndependentFromOrder(t *testing.T) {
	a := &stubRecognizer{name: "A"}
	b := &stubRecognizer{name: "B"}
	components := newRecognitionComponents(RecognitionPlan{
		Ordered:    []Recognizer{a, b},
		Confirmer:  b,
		Continuity: b,
	})
	if components.confirmer != b {
		t.Fatal("expected explicit confirmer to be preserved")
	}
	if components.continuity != b {
		t.Fatal("expected explicit continuity recognizer to be preserved")
	}
}
