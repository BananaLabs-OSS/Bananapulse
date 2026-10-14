//go:build wasip1

package main

import "github.com/BananaLabs-OSS/Fiber/pulp"

func main() {}
func init() {
	pulp.OnInit(func(_ []byte) error { pulp.Provide("game-server.v1.normalize", normalizeWire); return nil })
}
