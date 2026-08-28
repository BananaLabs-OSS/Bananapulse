package main

import (
	"log"

	_ "github.com/BananaLabs-OSS/Pulp-ext-sqlite"
)

// This binary supplies only the privileged extensions declared by the
// Bananapulse owner cells. Its loopback HTTP bridge translates the narrow,
// internal JSON compatibility surface into Lua-coordinated owner calls; it
// does not own monitor or subscriber state.
func main() {
	if err := runBridgeMain(); err != nil {
		log.Fatal(err)
	}
}
