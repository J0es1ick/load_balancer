package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/J0es1ick/cloud_test_assignment/internal/config"
	"github.com/J0es1ick/cloud_test_assignment/internal/observability"
	"gopkg.in/yaml.v2"
)

func command(args []string) (bool, error) {
	if len(args) == 0 {
		return false, nil
	}
	if args[0] == "version" {
		return true, json.NewEncoder(os.Stdout).Encode(map[string]string{"version": observability.Version, "commit": observability.Commit, "build_date": observability.BuildDate})
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	path := flags.String("config", os.Getenv("CONFIG_PATH"), "configuration file")
	if err := flags.Parse(args[1:]); err != nil {
		return true, err
	}
	if flags.NArg() != 0 {
		return true, fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	switch args[0] {
	case "serve", "validate", "print-effective-config", "discover":
	default:
		return true, fmt.Errorf("usage: balancer [serve|validate|print-effective-config|discover] -config <file>, or balancer version")
	}
	if *path == "" {
		return true, fmt.Errorf("provide -config or CONFIG_PATH")
	}
	cfg, err := config.Load(*path)
	if err != nil {
		return true, err
	}
	switch args[0] {
	case "serve":
		if err := os.Setenv("CONFIG_PATH", *path); err != nil {
			return true, err
		}
		return true, run()
	case "validate":
		fmt.Fprintln(os.Stdout, "configuration is valid (schema and references; reachability is checked at startup)")
		return true, nil
	case "print-effective-config":
		data, err := yaml.Marshal(cfg)
		if err != nil {
			return true, err
		}
		_, err = os.Stdout.Write(data)
		return true, err
	case "discover":
		return true, runDiscoveryController(cfg)
	}
	return true, nil
}
