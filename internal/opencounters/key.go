package opencounters

import (
	"bufio"
	"os"
	"regexp"
	"strings"
)

var (
	ownerPattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,39}$`)
	repoPattern  = regexp.MustCompile(`^[A-Za-z0-9._-]{1,100}$`)
)

func ParsePath(rest string) (key, ext string, ok bool) {
	const prefix = "github.com/"
	if !strings.HasPrefix(strings.ToLower(rest), prefix) {
		return "", "", false
	}
	path := rest[len(prefix):]
	for _, candidate := range []string{"svg", "gif"} {
		suffix := "." + candidate
		if strings.HasSuffix(strings.ToLower(path), suffix) {
			path = path[:len(path)-len(suffix)]
			ext = candidate
			break
		}
	}
	if strings.HasSuffix(strings.ToLower(path), ".svg") || strings.HasSuffix(strings.ToLower(path), ".gif") || strings.HasSuffix(strings.ToLower(path), ".png") {
		return "", "", false
	}
	parts := strings.Split(path, "/")
	if len(parts) < 1 || len(parts) > 2 || !ownerPattern.MatchString(parts[0]) {
		return "", "", false
	}
	if len(parts) == 2 && (!repoPattern.MatchString(parts[1]) || parts[1] == "." || parts[1] == "..") {
		return "", "", false
	}
	return prefix + strings.ToLower(path), ext, true
}

func LoadBlocklist(path string) (map[string]bool, error) {
	blocked := make(map[string]bool)
	if strings.TrimSpace(path) == "" {
		return blocked, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.ToLower(strings.TrimSpace(scanner.Text()))
		if line != "" && !strings.HasPrefix(line, "#") {
			blocked[line] = true
		}
	}
	return blocked, scanner.Err()
}
