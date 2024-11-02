package cif

import (
	"github.com/travigo/travigo/pkg/util"
)

func IsValidPassengerJourney(category string, atoc string) bool {
	// ATOC LT - London Underground
	// ATOC ZZ - Obfusucated
	return !util.ContainsString([]string{"LT", "ZZ", ""}, atoc) && util.ContainsString([]string{
		"OO", "OW",
		"XC", "XD", "XI", "XR", "XX", "XZ",
		"BR",
	}, category)
}
