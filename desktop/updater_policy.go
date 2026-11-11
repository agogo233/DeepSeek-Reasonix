package main

// desktopUpdaterEnabled is a build capability, not a user preference. Only an
// exact stable release build may contact or execute the production updater.
func desktopUpdaterEnabled() bool {
	// Fork-level privacy opt-out: never contact or execute the production updater.
	return false
}
