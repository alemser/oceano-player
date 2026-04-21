package main

import (
	"regexp"
	"strings"
)

var (
	parenSuffixRE = regexp.MustCompile(`\s*[\(\[].*?[\)\]]\s*`)
	nonWordRE     = regexp.MustCompile(`[^a-z0-9]+`)
	wordTokenRE   = regexp.MustCompile(`[a-z0-9]+`)
)

func normalizeTrackPart(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = parenSuffixRE.ReplaceAllString(s, " ")
	s = nonWordRE.ReplaceAllString(s, "")
	return s
}

func artistTokenSet(s string) map[string]struct{} {
	s = strings.ToLower(strings.TrimSpace(s))
	s = parenSuffixRE.ReplaceAllString(s, " ")
	tokens := wordTokenRE.FindAllString(s, -1)
	ignore := map[string]struct{}{
		"the": {}, "and": {}, "feat": {}, "featuring": {},
		"group": {}, "band": {}, "orchestra": {}, "ensemble": {},
		"quartet": {}, "trio": {}, "choir": {},
	}
	set := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		if _, skip := ignore[token]; skip {
			continue
		}
		set[token] = struct{}{}
	}
	return set
}

func tokenSetSubset(a, b map[string]struct{}) bool {
	if len(a) == 0 || len(a) > len(b) {
		return false
	}
	for token := range a {
		if _, ok := b[token]; !ok {
			return false
		}
	}
	return true
}

func artistsEquivalent(a, b string) bool {
	aNorm := normalizeTrackPart(a)
	bNorm := normalizeTrackPart(b)
	if aNorm == "" || bNorm == "" {
		return false
	}
	if aNorm == bNorm {
		return true
	}
	aTokens := artistTokenSet(a)
	bTokens := artistTokenSet(b)
	if len(aTokens) == 0 || len(bTokens) == 0 {
		return false
	}
	if len(aTokens) == len(bTokens) && tokenSetSubset(aTokens, bTokens) {
		return true
	}
	shorter := aTokens
	longer := bTokens
	if len(shorter) > len(longer) {
		shorter, longer = longer, shorter
	}
	return len(shorter) >= 2 && tokenSetSubset(shorter, longer)
}

func tracksEquivalent(aTitle, aArtist, bTitle, bArtist string) bool {
	aT := normalizeTrackPart(aTitle)
	bT := normalizeTrackPart(bTitle)
	if aT == "" || bT == "" {
		return false
	}
	return aT == bT && artistsEquivalent(aArtist, bArtist)
}

func sameTrackByProviderIDs(a, b *RecognitionResult) bool {
	if a == nil || b == nil {
		return false
	}
	if a.ACRID != "" && b.ACRID != "" {
		return a.ACRID == b.ACRID
	}
	if a.ShazamID != "" && b.ShazamID != "" {
		return a.ShazamID == b.ShazamID
	}
	return tracksEquivalent(a.Title, a.Artist, b.Title, b.Artist)
}

func sameTrackForStateContinuity(a, b *RecognitionResult) bool {
	if a == nil || b == nil {
		return false
	}
	if sameTrackByProviderIDs(a, b) {
		return true
	}
	return tracksEquivalent(a.Title, a.Artist, b.Title, b.Artist)
}

func canonicalTrackKey(r *RecognitionResult) string {
	if r == nil {
		return ""
	}
	if r.ACRID != "" {
		return "acrid:" + r.ACRID
	}
	if r.ShazamID != "" {
		return "shazam:" + r.ShazamID
	}
	return "meta:" + normalizeTrackPart(r.Title) + "|" + normalizeTrackPart(r.Artist)
}

func chooseConfirmationResult(
	primaryName string,
	primaryRes *RecognitionResult,
	primaryErr error,
	confirmName string,
	confirmRes *RecognitionResult,
	confirmErr error,
) (*RecognitionResult, error, string) {
	if confirmErr == nil && confirmRes != nil {
		return confirmRes, nil, confirmName
	}
	if primaryErr == nil && primaryRes != nil {
		return primaryRes, nil, primaryName
	}
	if confirmErr != nil {
		return nil, confirmErr, confirmName
	}
	if primaryErr != nil {
		return nil, primaryErr, primaryName
	}
	return nil, nil, ""
}
