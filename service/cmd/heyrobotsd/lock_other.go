//go:build !unix

package main

func lockData(string) error { return nil }

func dataInUse(string) bool { return false }
