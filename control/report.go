// Copyright 2014 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package control

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	barChar = "∎"
)

type result struct {
	errStr   string
	duration time.Duration
	happened time.Time
}

type report struct {
	avgTotal float64
	fastest  float64
	slowest  float64
	average  float64
	stddev   float64
	rps      float64

	results chan result
	total   time.Duration

	errorDist map[string]int

	// latencies in seconds
	lats []float64

	sps *secondPoints

	cfg Config
}

func printReport(results chan result, cfg Config) <-chan struct{} {
	return wrapReport(func() {
		r := &report{
			results:   results,
			errorDist: make(map[string]int),
			sps:       newSecondPoints(),
			cfg:       cfg,
		}
		r.finalize()
		r.print()
	})
}

func wrapReport(f func()) <-chan struct{} {
	donec := make(chan struct{})
	go func() {
		defer close(donec)
		f()
	}()
	return donec
}

func (r *report) finalize() {
	plog.Printf("finalize has started")
	st := time.Now()
	for res := range r.results {
		if res.errStr != "" {
			r.errorDist[res.errStr]++
		} else {
			r.sps.Add(res.happened, res.duration)
			r.lats = append(r.lats, res.duration.Seconds())
			r.avgTotal += res.duration.Seconds()
		}
	}
	r.total = time.Since(st)

	r.rps = float64(len(r.lats)) / r.total.Seconds()
	r.average = r.avgTotal / float64(len(r.lats))
	for i := range r.lats {
		dev := r.lats[i] - r.average
		r.stddev += dev * dev
	}
	r.stddev = math.Sqrt(r.stddev / float64(len(r.lats)))
	plog.Printf("finalize has finished")
}

func (r *report) print() {
	plog.Println("printing", len(r.lats), "results")
	sort.Float64s(r.lats)

	if len(r.lats) > 0 {
		r.fastest = r.lats[0]
		r.slowest = r.lats[len(r.lats)-1]
		fmt.Printf("\nSummary:\n")
		fmt.Printf("  Total:\t%4.4f secs.\n", r.total.Seconds())
		fmt.Printf("  Slowest:\t%4.4f secs.\n", r.slowest)
		fmt.Printf("  Fastest:\t%4.4f secs.\n", r.fastest)
		fmt.Printf("  Average:\t%4.4f secs.\n", r.average)
		fmt.Printf("  Stddev:\t%4.4f secs.\n", r.stddev)
		fmt.Printf("  Requests/sec:\t%4.4f\n", r.rps)

		fmt.Printf("\n")
		r.printLatencyDistribution()
		fmt.Printf("\n")
		r.printHistogram()
		fmt.Printf("\n")
		r.printLatencies()
		fmt.Printf("\n")
		r.printSecondSample()
		fmt.Printf("\n")
	}

	plog.Println("ERROR COUNT:", r.errorDist)
}

// Prints percentile latencies.
func (r *report) printLatencies() {
	pctls := []int{10, 25, 50, 75, 90, 95, 99}
	data := make([]float64, len(pctls))
	j := 0
	for i := 0; i < len(r.lats) && j < len(pctls); i++ {
		current := i * 100 / len(r.lats)
		if current >= pctls[j] {
			data[j] = r.lats[i]
			j++
		}
	}
	fmt.Printf("\nLatency distribution:\n")
	for i := 0; i < len(pctls); i++ {
		if data[i] > 0 {
			fmt.Printf("  %v%% in %4.4f secs.\n", pctls[i], data[i])
		}
	}
}

func (r *report) printSecondSample() {
	plog.Println("getTimeSeries starts for", len(r.sps.tm), "points")
	txt := r.sps.getTimeSeries().String()
	plog.Println("getTimeSeries finished for", len(r.sps.tm), "points")
	fmt.Println(txt)

	plog.Println("saving time series at", r.cfg.ResultPathTimeSeries)
	if err := toFile(txt, r.cfg.ResultPathTimeSeries); err != nil {
		plog.Fatal(err)
	}
	plog.Println("saved time series at", r.cfg.ResultPathTimeSeries)
}

// printLatencyDistribution prints latency distribution by 10ms.
func (r *report) printLatencyDistribution() {
	plog.Printf("analyzing latency distribution of %d points", len(r.lats))
	min := math.MaxFloat64
	max := -100000.0
	rm := make(map[float64]int)
	for _, lt := range r.lats {
		// convert second(float64) to millisecond
		ms := lt * 1000

		// truncate all digits below 10ms
		// (e.g. 125.11ms becomes 120ms)
		v := math.Trunc(ms/10) * 10
		if _, ok := rm[v]; !ok {
			rm[v] = 1
		} else {
			rm[v]++
		}

		if min > v {
			min = v
		}
		if max < v {
			max = v
		}
	}

	cur := min
	for cur != max {
		v, ok := rm[cur]
		if ok {
			fmt.Printf("%dms: %d\n", int64(cur), v)
		} else {
			fmt.Printf("%dms: 0\n", int64(cur))
		}
		cur += 10
	}
}

func (r *report) printHistogram() {
	bc := 10
	buckets := make([]float64, bc+1)
	counts := make([]int, bc+1)
	bs := (r.slowest - r.fastest) / float64(bc)
	for i := 0; i < bc; i++ {
		buckets[i] = r.fastest + bs*float64(i)
	}
	buckets[bc] = r.slowest
	var bi int
	var max int
	for i := 0; i < len(r.lats); {
		if r.lats[i] <= buckets[bi] {
			i++
			counts[bi]++
			if max < counts[bi] {
				max = counts[bi]
			}
		} else if bi < len(buckets)-1 {
			bi++
		}
	}
	fmt.Printf("\nResponse time histogram:\n")
	for i := 0; i < len(buckets); i++ {
		// Normalize bar lengths.
		var barLen int
		if max > 0 {
			barLen = counts[i] * 40 / max
		}
		fmt.Printf("  %4.3f [%v]\t|%v\n", buckets[i], counts[i], strings.Repeat(barChar, barLen))
	}
}

func (r *report) printErrors() {
	fmt.Printf("\nError distribution:\n")
	for err, num := range r.errorDist {
		fmt.Printf("  [%d]\t%s\n", num, err)
	}
}
