package samp

import "testing"

func TestAuthKeyKnownVectors(t *testing.T) {
	vectors := map[string]string{
		"7B6F9203EF3C4D5A":     "8EDBD6DD3A0C86F49D4751B1151587BB28181C89",
		"226B4F982407735D":     "F8A72CD3326708AC5FAC9571759DB6E305E2AB8E",
		"226B4F982407735D\x00": "F8A72CD3326708AC5FAC9571759DB6E305E2AB8E",
	}
	for challenge, want := range vectors {
		if got := AuthKey(challenge); got != want {
			t.Errorf("AuthKey(%q)=%q want %q", challenge, got, want)
		}
	}
}
