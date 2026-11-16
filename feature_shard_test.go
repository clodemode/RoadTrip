package main

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The scenarios run in SHARDS: the gate builds the test binary once and starts
// it several times side by side, each process told FEATURE_SHARD=i/n and each
// running its own slice of features/*.feature. Processes, not goroutines,
// because the scenarios were written to run alone: every one sets PUSH_TOKEN
// in the environment and hangs its cleanup on the one *testing.T. A process
// boundary keeps them alone and costs nothing to prove.
//
// The slices are a partition, by construction and by test
// (TestFeatureShardsPartitionEveryFeature): every feature file is in exactly
// one shard for every n, so n shards run exactly the scenarios one run does.

// featureShards splits the feature files into n slices of roughly equal
// scenario count. The split depends only on the files, so every process of a
// run computes the same one.
func featureShards(files []string, weight map[string]int, n int) [][]string {
	sorted := append([]string(nil), files...)
	// Heaviest first, name as the tie-break, each to the lightest shard so far.
	sort.Slice(sorted, func(i, j int) bool {
		if weight[sorted[i]] != weight[sorted[j]] {
			return weight[sorted[i]] > weight[sorted[j]]
		}
		return sorted[i] < sorted[j]
	})
	shards := make([][]string, n)
	load := make([]int, n)
	for _, f := range sorted {
		k := 0
		for i := 1; i < n; i++ {
			if load[i] < load[k] {
				k = i
			}
		}
		shards[k] = append(shards[k], f)
		load[k] += weight[f] + 1
	}
	for _, s := range shards {
		sort.Strings(s)
	}
	return shards
}

// scenarioWeight counts the scenarios a feature file declares. An outline
// counts once per example row; the count only balances the shards, it is not
// the census.
func scenarioWeight(path string) (int, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, inExamples, header := 0, false, false
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case strings.HasPrefix(line, "Scenario Outline:"), strings.HasPrefix(line, "Scenario Template:"):
			inExamples = false
		case strings.HasPrefix(line, "Scenario:"), strings.HasPrefix(line, "Example:"):
			inExamples = false
			n++
		case strings.HasPrefix(line, "Examples:"), strings.HasPrefix(line, "Scenarios:"):
			inExamples, header = true, true
		case inExamples && strings.HasPrefix(line, "|"):
			if header {
				header = false
			} else {
				n++
			}
		}
	}
	return n, sc.Err()
}

// featureFiles lists features/*.feature with each file's scenario weight.
func featureFiles() ([]string, map[string]int, error) {
	files, err := filepath.Glob(filepath.Join("features", "*.feature"))
	if err != nil {
		return nil, nil, err
	}
	sort.Strings(files)
	weight := map[string]int{}
	for _, f := range files {
		w, err := scenarioWeight(f)
		if err != nil {
			return nil, nil, err
		}
		weight[f] = w
	}
	return files, weight, nil
}

// parseShard reads "i/n" with 1 <= i <= n <= 64.
func parseShard(v string) (i, n int, err error) {
	a, b, ok := strings.Cut(v, "/")
	if ok {
		i, err = strconv.Atoi(a)
		if err == nil {
			n, err = strconv.Atoi(b)
		}
	}
	if !ok || err != nil || n < 1 || n > 64 || i < 1 || i > n {
		return 0, 0, fmt.Errorf("FEATURE_SHARD=%q is not i/n with 1 <= i <= n <= 64", v)
	}
	return i, n, nil
}

// featurePaths is what TestFeatures hands godog: the whole features directory,
// or this process's shard of it. Anything it cannot read as a shard, and a
// shard that would hold no feature, fails the test: a shard that ran nothing
// must never read as a shard that passed.
func featurePaths(t *testing.T) []string {
	t.Helper()
	v := os.Getenv("FEATURE_SHARD")
	if v == "" {
		return []string{"features"}
	}
	i, n, err := parseShard(v)
	if err != nil {
		t.Fatal(err)
	}
	files, weight, err := featureFiles()
	if err != nil {
		t.Fatalf("list the features: %v", err)
	}
	mine := featureShards(files, weight, n)[i-1]
	if len(mine) == 0 {
		t.Fatalf("FEATURE_SHARD=%s holds no feature file (%d files, %d shards): use fewer shards", v, len(files), n)
	}
	return mine
}

// Every feature file is in exactly one shard, for every shard count the gate
// may ask for: the shards run what one run runs, no more and no less.
func TestFeatureShardsPartitionEveryFeature(t *testing.T) {
	files, weight, err := featureFiles()
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no feature file was found, so nothing was checked")
	}
	for n := 1; n <= 16; n++ {
		seen := map[string]int{}
		for _, s := range featureShards(files, weight, n) {
			for _, f := range s {
				seen[f]++
			}
		}
		for _, f := range files {
			if seen[f] != 1 {
				t.Errorf("with %d shards %s is in %d of them", n, f, seen[f])
			}
		}
		if len(seen) != len(files) {
			t.Errorf("with %d shards the shards hold %d files, the directory %d", n, len(seen), len(files))
		}
	}
}

func TestFeatureShardRefusesWhatItCannotRead(t *testing.T) {
	for _, v := range []string{"0/4", "5/4", "x/4", "2", "1/0", "1/65", "/", "1/2/3"} {
		if _, _, err := parseShard(v); err == nil {
			t.Errorf("FEATURE_SHARD=%q was accepted", v)
		}
	}
	if i, n, err := parseShard("3/8"); err != nil || i != 3 || n != 8 {
		t.Errorf("3/8 read as %d/%d, %v", i, n, err)
	}
}
