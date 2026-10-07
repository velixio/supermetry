// Package main refreshes the public enrichment datasets embedded by Panel.
package main

//go:generate go run .

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

const (
	crawlerURL = "https://raw.githubusercontent.com/monperrus/crawler-user-agents/master/crawler-user-agents.json"
	spamURL    = "https://raw.githubusercontent.com/matomo-org/referrer-spam-list/master/spammers.txt"
)

var client = &http.Client{Timeout: 2 * time.Minute}

func main() {
	must(writeCrawlerPatterns())
	must(writeURL("referrer-spam.txt", spamURL))

	month := time.Now().UTC().Format("2006-01")
	dbURL := "https://download.db-ip.com/free/dbip-country-lite-" + month + ".mmdb.gz"
	must(writeGzipURL("dbip-country-lite.mmdb", dbURL))
}

func writeURL(path, source string) error {
	data, err := fetch(source)
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

func writeGzipURL(path, source string) error {
	data, err := fetch(source)
	if err != nil {
		return err
	}
	reader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open %s: %w", source, err)
	}
	defer reader.Close()
	decoded, err := io.ReadAll(io.LimitReader(reader, 64<<20))
	if err != nil {
		return fmt.Errorf("decompress %s: %w", source, err)
	}
	return os.WriteFile(path, decoded, 0o644)
}

func writeCrawlerPatterns() error {
	data, err := fetch(crawlerURL)
	if err != nil {
		return err
	}
	var entries []struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(data, &entries); err != nil {
		return fmt.Errorf("decode %s: %w", crawlerURL, err)
	}
	data, err = json.MarshalIndent(entries, "", "  ")
	if err != nil {
		return fmt.Errorf("encode crawler patterns: %w", err)
	}
	data = append(data, '\n')
	return os.WriteFile("crawler-user-agents.json", data, 0o644)
}

func fetch(source string) ([]byte, error) {
	response, err := client.Get(source)
	if err != nil {
		return nil, fmt.Errorf("fetch %s: %w", source, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch %s: %s", source, response.Status)
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, 128<<20))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", source, err)
	}
	return data, nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
