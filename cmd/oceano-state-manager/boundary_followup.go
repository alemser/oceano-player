package main

import (
	"log"

	internallibrary "github.com/alemser/oceano-player/internal/library"
)

func recognitionFollowupNewRecording(pre, post *RecognitionResult) *bool {
	if post == nil {
		return nil
	}
	if pre == nil {
		v := true
		return &v
	}
	same := false
	if pre.ACRID != "" && post.ACRID != "" {
		same = pre.ACRID == post.ACRID
	} else if pre.ShazamID != "" && post.ShazamID != "" {
		same = pre.ShazamID == post.ShazamID
	}
	v := !same
	return &v
}

func (c *recognitionCoordinator) linkBoundaryFollowup(isBoundaryTrigger bool, evID int64, fu internallibrary.BoundaryRecognitionFollowup) {
	if c.lib == nil || !isBoundaryTrigger || evID <= 0 {
		return
	}
	if err := c.lib.LinkBoundaryRecognitionFollowup(evID, fu); err != nil {
		log.Printf("boundary follow-up id=%d: %v", evID, err)
	}
}
