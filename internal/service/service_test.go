package service

import "testing"

func TestInstallRejectsEmptyPaths(t *testing.T) {
	if _, err := Install(nil, "", ""); err == nil {
		t.Fatal("want an error without paths")
	}
}
