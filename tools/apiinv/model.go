package main

import "supermetry/internal/apiroutes"

type Route = apiroutes.Route
type Operation = apiroutes.Operation
type Inventory = apiroutes.Inventory
type hitSet = apiroutes.HitSet

func collectRoutes(dir string) ([]Route, []string, error) {
	return apiroutes.Collect(dir)
}

func readOpenAPI(filename string) ([]Operation, error) {
	return apiroutes.ReadOpenAPI(filename)
}

func readHits(filename string) (hitSet, error) {
	return apiroutes.ReadHits(filename)
}

func mergeInventory(routes []Route, operations []Operation, hits hitSet) Inventory {
	return apiroutes.MergeInventory(routes, operations, hits)
}
