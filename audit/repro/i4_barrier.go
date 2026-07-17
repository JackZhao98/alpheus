// Command i4_barrier reproduces the daily-trade TOCTOU probe with a true
// in-process start barrier. Process-per-request tools such as xargs can add
// enough launch jitter to hide the vulnerable count-to-insert window.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type result struct {
	Class  string `json:"class"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

type dayLedger struct {
	TradesToday int `json:"trades_today"`
}

type stateResult struct {
	Day struct {
		Live   dayLedger `json:"live"`
		Shadow dayLedger `json:"shadow"`
	} `json:"day"`
}

func main() {
	baseURLs := flag.String("urls", "http://localhost:8100", "comma-separated kernel base URLs")
	shadow := flag.Bool("shadow", false, "probe the shadow ledger")
	seed := flag.Int("seed", 5, "sequential Class-B operations before the barrier")
	requests := flag.Int("requests", 20, "simultaneous requests released by the barrier")
	flag.Parse()
	if *seed < 0 || *requests < 1 {
		fmt.Fprintln(os.Stderr, "seed must be >= 0 and requests must be >= 1")
		os.Exit(2)
	}
	urls := []string{}
	for _, url := range strings.Split(*baseURLs, ",") {
		if url = strings.TrimRight(strings.TrimSpace(url), "/"); url != "" {
			urls = append(urls, url)
		}
	}
	if len(urls) == 0 {
		fmt.Fprintln(os.Stderr, "at least one kernel URL is required")
		os.Exit(2)
	}

	transport := &http.Transport{MaxIdleConns: *requests + 2, MaxIdleConnsPerHost: *requests + 2}
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	payload, err := json.Marshal(map[string]any{
		"proposer": "audit-i4", "action": "open", "kind": "equity",
		"underlying": "I4", "symbol": "I4", "side": "buy", "qty": 0.01,
		"limit": 100.1, "max_risk_usd": 1.001, "shadow": *shadow,
		"plan": map[string]string{
			"stop": "90", "invalidation": "x", "time_stop": "15:45", "target": "120",
		},
	})
	if err != nil {
		panic(err)
	}
	quote, err := json.Marshal(map[string]any{
		"symbol": "I4", "bid": 100, "ask": 100.1, "open_interest": 1000,
	})
	if err != nil {
		panic(err)
	}
	for _, url := range urls {
		if err := postQuote(client, url+"/sim/quote", quote); err != nil {
			fmt.Fprintf(os.Stderr, "seed quote: %v\n", err)
			os.Exit(1)
		}
	}

	for i := 0; i < *seed; i++ {
		res := post(client, urls[i%len(urls)]+"/operations", payload)
		if res.Error != "" || res.Class != "B" {
			fmt.Fprintf(os.Stderr, "seed %d failed: %+v\n", i+1, res)
			os.Exit(1)
		}
	}

	ready := sync.WaitGroup{}
	ready.Add(*requests)
	start := make(chan struct{})
	results := make(chan result, *requests)
	for i := 0; i < *requests; i++ {
		go func(index int) {
			ready.Done()
			<-start
			results <- post(client, urls[index%len(urls)]+"/operations", payload)
		}(i)
	}
	ready.Wait()
	close(start)

	summary := map[string]int{}
	for i := 0; i < *requests; i++ {
		res := <-results
		key := res.Class + "/" + res.Status
		if res.Error != "" {
			key = "error/" + res.Error
		}
		summary[key]++
	}

	state := stateResult{}
	resp, err := client.Get(urls[0] + "/state")
	if err == nil {
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			err = fmt.Errorf("state endpoint: %s", resp.Status)
		} else {
			err = json.NewDecoder(resp.Body).Decode(&state)
		}
	}
	tradesToday := state.Day.Live.TradesToday
	if *shadow {
		tradesToday = state.Day.Shadow.TradesToday
	}
	expectedB := 1
	expectedC := *requests - expectedB
	expectedTrades := *seed + expectedB
	passed := err == nil &&
		summary["B/auto_approved"]+summary["B/executed"] == expectedB &&
		summary["C/pending_review"] == expectedC &&
		tradesToday == expectedTrades
	verdict := "FAIL"
	if passed {
		verdict = "PASS"
	}
	output := map[string]any{
		"verdict": verdict, "urls": urls, "shadow": *shadow, "seed": *seed,
		"requests": *requests, "results": summary, "state": state,
		"assertions": map[string]any{
			"expected_b": expectedB, "expected_c": expectedC,
			"expected_trades_today": expectedTrades, "actual_trades_today": tradesToday,
		},
	}
	_ = json.NewEncoder(os.Stdout).Encode(output)
	if !passed {
		if err != nil {
			fmt.Fprintf(os.Stderr, "FAIL: %v\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "FAIL: expected B=%d C=%d trades_today=%d\n", expectedB, expectedC, expectedTrades)
		}
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "PASS: B=%d C=%d trades_today=%d\n", expectedB, expectedC, tradesToday)
}

func postQuote(client *http.Client, url string, payload []byte) error {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s: %s", url, resp.Status)
	}
	return nil
}

func post(client *http.Client, url string, payload []byte) result {
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(payload))
	if err != nil {
		return result{Error: err.Error()}
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		return result{Error: err.Error()}
	}
	defer resp.Body.Close()
	var out result
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		out.Error = err.Error()
	}
	if resp.StatusCode != http.StatusOK && out.Error == "" {
		out.Error = resp.Status
	}
	return out
}
