package main

import (
	"context"
	"flag"
	"log"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/meet-glen-ai/terraform-provider-glen/internal/provider"
)

var version = "dev"

func main() {
	debug := flag.Bool("debug", false, "run with support for debuggers such as delve")
	flag.Parse()
	err := providerserver.Serve(context.Background(), provider.New(version), providerserver.ServeOpts{
		Address: "registry.terraform.io/meet-glen-ai/glen",
		Debug:   *debug,
	})
	if err != nil {
		log.Fatal(err)
	}
}
