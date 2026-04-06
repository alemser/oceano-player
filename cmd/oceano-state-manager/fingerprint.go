package main

import internalrecognition "github.com/alemser/oceano-player/internal/recognition"

type Fingerprint = internalrecognition.Fingerprint
type Fingerprinter = internalrecognition.Fingerprinter

var ParseFingerprint = internalrecognition.ParseFingerprint
var BER = internalrecognition.BER
var GenerateFingerprints = internalrecognition.GenerateFingerprints

func newFingerprinter() Fingerprinter {
	return internalrecognition.NewFingerprinter()
}
