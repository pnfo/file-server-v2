package main

import singlish "github.com/pnfo/singlish-search"

// getPossibleMatches returns the Sinhala spellings a Singlish (or mixed) query could stand for, most likely
// first (at most 300). Same output as getPossibleMatches in the JS @pnfo/singlish-search.
func getPossibleMatches(q string) []string { return singlish.GetPossibleMatches(q) }
