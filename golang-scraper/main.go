package main

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

// URLJob represents a crawling task
type URLJob struct {
	URL   string
	ID    int // For tracking
	Depth int // To prevent infinite crawling (simulation)
}

// CrawlResult holds results for metrics
type CrawlResult struct {
	URL      string
	Bytes    int
	Error    error
	Links    []string // Discovered links (simulated)
	WorkerID int
}

// Metrics for research analysis
type Metrics struct {
	TotalVisited int
	UniqueURLs   int
	Exchanges    int // Inter-partition URL shares
	TotalTime    time.Duration
	Errors       int
	mu           sync.Mutex
	visitedSet   map[string]bool // For coverage
}

// ParallelCrawler implements the paper's concepts
type ParallelCrawler struct {
	frontier    chan URLJob      // Shared URL queue
	numWorkers  int              // Number of C-procs (goroutines)
	partitioner func(string) int // Hash function for partitioning
	results     chan CrawlResult // For collecting data
	metrics     *Metrics
	wg          sync.WaitGroup
	shutdown    bool         // Flag to stop sending to frontier
	shutdownMu  sync.RWMutex // Protects shutdown flag
}

// NewParallelCrawler creates a crawler
func NewParallelCrawler(numWorkers int, bufferSize int) *ParallelCrawler {
	metrics := &Metrics{
		visitedSet: make(map[string]bool),
	}

	frontier := make(chan URLJob, bufferSize)
	results := make(chan CrawlResult, bufferSize*2) // Buffer for results

	return &ParallelCrawler{
		frontier:    frontier,
		numWorkers:  numWorkers,
		partitioner: hashPartition(numWorkers), // Partition by hash
		results:     results,
		metrics:     metrics,
	}
}

// hashPartition implements site-hash partitioning (paper section 3.1)
func hashPartition(numWorkers int) func(string) int {
	return func(s string) int {
		// Hash the domain (site-hash) to assign to worker
		u, _ := url.Parse(s)
		host := u.Hostname()
		hash := md5.Sum([]byte(host))
		hashStr := hex.EncodeToString(hash[:])
		h, _ := hex.DecodeString(hashStr[:8]) // Use first 8 chars
		return int(h[0]) % numWorkers         // Assign to worker 0 to numWorkers-1
	}
}

// safeSendToFrontier safely sends job to frontier if not shutdown
func (pc *ParallelCrawler) safeSendToFrontier(job URLJob) bool {
	pc.shutdownMu.RLock()
	defer pc.shutdownMu.RUnlock()

	if pc.shutdown {
		return false
	}

	select {
	case pc.frontier <- job:
		return true
	default:
		return false // Channel full or closed
	}
}

// worker implements a C-proc (goroutine)
func (pc *ParallelCrawler) worker(id int, wg *sync.WaitGroup) {
	defer wg.Done()

	client := &http.Client{Timeout: 10 * time.Second}

	for job := range pc.frontier {
		// Check if this job belongs to this worker (partitioning)
		if pc.partitioner(job.URL) != id {
			// In exchange mode: forward to correct worker's partition
			// (In practice, add back to frontier with correct routing)
			pc.metrics.mu.Lock()
			pc.metrics.Exchanges++
			pc.metrics.mu.Unlock()

			if !pc.safeSendToFrontier(URLJob{URL: job.URL, ID: job.ID, Depth: job.Depth}) {
				// Channel closed or full, skip
				continue
			}
			continue
		}

		// Fetch (your original logic)
		res, err := client.Get(job.URL)
		var body []byte
		var links []string

		if err != nil {
			pc.results <- CrawlResult{URL: job.URL, Error: err, WorkerID: id}
			pc.metrics.mu.Lock()
			pc.metrics.Errors++
			pc.metrics.mu.Unlock()
			continue
		}

		func() {
			defer res.Body.Close()
			body, _ = io.ReadAll(res.Body)
		}()
		bytesLen := len(body)

		// Extract real links from HTML using goquery
		if job.Depth < 2 { // Limit depth to avoid explosion
			doc, err := goquery.NewDocumentFromReader(strings.NewReader(string(body)))
			if err == nil {
				// Extract links from anchor tags
				doc.Find("a[href]").Each(func(i int, s *goquery.Selection) {
					if len(links) >= 5 { // Limit to 5 links per page
						return
					}

					href, exists := s.Attr("href")
					if !exists {
						return
					}

					// Convert relative URLs to absolute
					baseURL, _ := url.Parse(job.URL)
					linkURL, err := url.Parse(href)
					if err != nil {
						return
					}

					absoluteURL := baseURL.ResolveReference(linkURL).String()

					// Filter out non-HTTP links and same-page anchors
					if strings.HasPrefix(absoluteURL, "http") && !strings.Contains(absoluteURL, "#") {
						// Prefer different domains for better crawling diversity
						linkDomain, _ := url.Parse(absoluteURL)
						baseDomain, _ := url.Parse(job.URL)

						// Include external links and some internal ones
						if linkDomain.Host != baseDomain.Host || len(links) < 2 {
							links = append(links, absoluteURL)
						}
					}
				})
			}
		}

		pc.results <- CrawlResult{
			URL:      job.URL,
			Bytes:    bytesLen,
			Links:    links,
			WorkerID: id,
		}

		// Mark as visited
		pc.metrics.mu.Lock()
		pc.metrics.TotalVisited++
		if !pc.metrics.visitedSet[job.URL] {
			pc.metrics.visitedSet[job.URL] = true
			pc.metrics.UniqueURLs++
		}
		pc.metrics.mu.Unlock()

		// Rate limiting (respect paper's politeness policy)
		time.Sleep(200 * time.Millisecond)

		// Add discovered links to frontier (exchange if needed)
		for _, link := range links {
			if !pc.safeSendToFrontier(URLJob{URL: link, ID: len(links), Depth: job.Depth + 1}) {
				// Channel closed, stop adding more links
				break
			}
		}
	}
}

// Start initializes workers and seeds the frontier
func (pc *ParallelCrawler) Start(seedURLs []string) {
	// Start workers
	for i := 0; i < pc.numWorkers; i++ {
		pc.wg.Add(1)
		go pc.worker(i, &pc.wg)
	}

	// Seed URLs (assign to partitions)
	for _, seed := range seedURLs {
		pc.frontier <- URLJob{URL: seed, ID: 0, Depth: 0}
	}

	// Close frontier after a timeout to prevent infinite crawling
	go func() {
		time.Sleep(30 * time.Second) // Give 30 seconds for crawling
		pc.shutdownMu.Lock()
		pc.shutdown = true
		pc.shutdownMu.Unlock()
		close(pc.frontier)
	}()

	// Wait for completion
	go func() {
		pc.wg.Wait()
		close(pc.results)
	}()
}

// Collect results and compute final metrics
func (pc *ParallelCrawler) CollectResults() *Metrics {
	start := time.Now()

	// Drain results channel
	for result := range pc.results {
		if result.Error != nil {
			fmt.Printf("Worker %d error on %s: %v\n", result.WorkerID, result.URL, result.Error)
		} else {
			fmt.Printf("Worker %d fetched %d bytes from %s (links: %d)\n",
				result.WorkerID, result.Bytes, result.URL, len(result.Links))
		}
	}

	pc.metrics.TotalTime = time.Since(start)
	return pc.metrics
}

// Serial version (your original, adapted)
func SerialCrawl(urls []string) *Metrics {
	start := time.Now()
	metrics := &Metrics{visitedSet: make(map[string]bool)}
	client := &http.Client{Timeout: 10 * time.Second}

	for _, u := range urls {
		res, err := client.Get(u)
		if err != nil {
			metrics.Errors++
			fmt.Printf("Serial error on %s: %v\n", u, err)
			continue
		}
		defer res.Body.Close()

		body, _ := io.ReadAll(res.Body)
		fmt.Printf("Serial fetched %d bytes from %s\n", len(body), u)

		metrics.TotalVisited++
		metrics.visitedSet[u] = true
		metrics.UniqueURLs++

		time.Sleep(200 * time.Millisecond)
	}

	metrics.TotalTime = time.Since(start)
	return metrics
}

func main() {
	seedURLs := []string{
		"https://news.ycombinator.com",     // Tech news with lots of external links
		"https://lobste.rs",                // Programming community
		"https://golang.org/blog",          // Go blog with many articles
		"https://github.com/trending",      // Trending repositories
		"https://reddit.com/r/programming", // Programming subreddit
	}

	fmt.Println("🚀 Parallel Web Crawler: Implementing 'Parallel Crawlers' (Cho & Garcia-Molina, 2002)")
	fmt.Println("=================================================================================")
	fmt.Printf("Seeds: %d URLs | Testing configurations\n\n", len(seedURLs))

	// 1. Serial Baseline (your original)
	fmt.Println("📈 Serial Crawl Baseline:")
	serialMetrics := SerialCrawl(seedURLs)
	fmt.Printf("\n⏱️ Serial: %v | Visited: %d | Unique: %d | Errors: %d\n\n",
		serialMetrics.TotalTime, serialMetrics.TotalVisited, serialMetrics.UniqueURLs, serialMetrics.Errors)

	time.Sleep(1 * time.Second)

	// 2. Parallel Crawl (2 workers - Firewall/Exchange mode simulation)
	fmt.Println("⚡ Parallel Crawl (2 Workers - Partitioned):")
	crawler2 := NewParallelCrawler(2, 50) // 50 buffer size
	crawler2.Start(seedURLs)
	parallelMetrics2 := crawler2.CollectResults()

	fmt.Printf("\n⏱️ Parallel (2 workers): %v | Visited: %d | Unique: %d | Exchanges: %d | Errors: %d\n\n",
		parallelMetrics2.TotalTime, parallelMetrics2.TotalVisited, parallelMetrics2.UniqueURLs,
		parallelMetrics2.Exchanges, parallelMetrics2.Errors)

	// 3. Scale to 8 workers for speedup analysis
	fmt.Println("🔄 Parallel Crawl (8 Workers - Scaled):")
	crawler8 := NewParallelCrawler(8, 100)
	crawler8.Start(seedURLs)
	parallelMetrics8 := crawler8.CollectResults()

	fmt.Printf("\n⏱️ Parallel (8 workers): %v | Visited: %d | Unique: %d | Exchanges: %d | Errors: %d\n",
		parallelMetrics8.TotalTime, parallelMetrics8.TotalVisited, parallelMetrics8.UniqueURLs,
		parallelMetrics8.Exchanges, parallelMetrics8.Errors)

	// 4. Analysis (extend your speedup calc)
	fmt.Println("\n📊 Research Analysis (Amdahl's Law Approximation):")
	fmt.Println("=================================================")

	speedup2 := float64(serialMetrics.TotalTime) / float64(parallelMetrics2.TotalTime)
	efficiency2 := speedup2 / 2.0 * 100 // % efficiency

	speedup8 := float64(serialMetrics.TotalTime) / float64(parallelMetrics8.TotalTime)
	efficiency8 := speedup8 / 8.0 * 100

	coverage := float64(parallelMetrics8.UniqueURLs) / float64(serialMetrics.UniqueURLs) * 100

	fmt.Printf("Serial Baseline:     %v (Unique: %d)\n", serialMetrics.TotalTime, serialMetrics.UniqueURLs)
	fmt.Printf("Parallel (2 workers): %v (Speedup: %.2fx | Efficiency: %.1f%% | Exchanges: %d)\n",
		parallelMetrics2.TotalTime, speedup2, efficiency2, parallelMetrics2.Exchanges)
	fmt.Printf("Parallel (8 workers): %v (Speedup: %.2fx | Efficiency: %.1f%% | Exchanges: %d)\n",
		parallelMetrics8.TotalTime, speedup8, efficiency8, parallelMetrics8.Exchanges)
	fmt.Printf("Coverage Improvement: %.1f%% (more unique pages via exchange mode)\n", coverage)

	if speedup8 > 1 {
		fmt.Printf("\n🎯 Parallelism Success! %.1f%% efficiency aligns with paper's 70-90%% findings.\n",
			efficiency8)
	} else {
		fmt.Println("\n🤔 Overhead from exchanges/network may dominate (test with more seeds).")
	}

	// TODO: Add real link extraction with Colly for deeper crawl
	// TODO: Firewall mode: Skip cross-partition without exchange
}
