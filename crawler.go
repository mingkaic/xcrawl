//// file: crawler.go
package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"net/url"

	"github.com/PuerkitoBio/purell"
	"github.com/mingkaic/crawler/crutils"
	"github.com/mingkaic/crawler/scrape"
	"golang.org/x/net/html"
	"gopkg.in/fatih/set.v0"
	"gopkg.in/yaml.v2"
)

type depthInfo struct {
	link  string
	depth uint
}

type crawlOpt struct {
	Search struct { // search constraints
		MaxDepth     uint     `yaml:"depth"`
		SameHost     bool     `yaml:"same_host"`
		ContainsTags []string `yaml:"contains_tags"`
	}
	Record struct { // options for recording
		Tags []string
		Attr string
	}
}

func main() {
	flag.Parse()
	args := flag.Args()
	if len(args) < 1 {
		log.Fatalf("Specify starting location")
	}

	// search parameter components
	cyml := flag.String("cyml", "reddit_crawl.yml", "yml file outlining search constraint")
	if cyml == nil {
		log.Fatalf("Constraint file not specified")
	}
	options, err := ioutil.ReadFile(*cyml)
	if err != nil {
		log.Fatalf("yaml file read error: %v", err)
	}
	opt := crawlOpt{}
	err = yaml.Unmarshal(options, &opt)
	fmt.Println("max depth:", opt.Search.MaxDepth)
	fmt.Println("visit same hostname only:", opt.Search.SameHost)
	fmt.Println("a tags must contain the following tags:", opt.Search.ContainsTags)
	if err != nil {
		log.Fatalf("yaml option error: %v", err)
	}

	// synchronization components
	queue := make(chan depthInfo)
	stopCh := make(chan struct{})
	goCount := crutils.AtomicInt(0)

	// optimization components
	visited := set.New()
	visited.Add(args[0])
	go func() {
		queue <- depthInfo{args[0], 0}
	}()

	go func() { // termination goroutine
		for range stopCh {
			if goCount.Decrement() == 0 { // stop condition
				close(queue)
				close(stopCh)
			}
		}
	}()
	for site := range queue {
		if site.depth <= opt.Search.MaxDepth {
			// propagate to linked sites
			goCount.Increment() // increment in main in case goroutine completes before main
			fmt.Println("fetching", site.link, "@ depth", site.depth)
			uriEnqueue(site.link, &opt,
				func(next_site string) {
					if !visited.Has(next_site) {
						visited.Add(next_site) // tag link as visited before to avoid duplicate
						goCount.Increment()    // spawning new go routine
						go func() {
							queue <- depthInfo{link: next_site, depth: site.depth + 1}
							stopCh <- struct{}{} // check termination goroutine for stop condition
						}()
					}
				})
			stopCh <- struct{}{} // check termination goroutine for stop condition
		}
	}
}

func uriEnqueue(uri string, opt *crawlOpt, linkHandle func(string)) {
	body, err := request(uri)
	if err != nil {
		fmt.Println(err.Error())
		return
	}
	go func() {
		// record assets
		attrVals := scrape.FindAttrVals(body, opt.Record.Attr, opt.Record.Tags...)
		fmt.Println(attrVals)
	}()
	findLink := scrape.FindAll("a")
	links := searchLinks(findLink(body), opt)
	for _, link := range links {
		validLink, err := resolveRef(uri, link, opt.Search.SameHost)
		if err == nil {
			linkHandle(validLink)
		}
	}
}

func request(link string) (body *html.Node, err error) {
	// disable ssl verification
	tlsConfig := &tls.Config{InsecureSkipVerify: true}
	transport := &http.Transport{TLSClientConfig: tlsConfig}
	client := &http.Client{Transport: transport}

	req, err := http.NewRequest("GET", link, nil)
	if err != nil {
		return
	}

	req.Header.Set("User-Agent", "Mozilla/5.0")
	resp, err := client.Do(req)
	if err != nil {
		return
	}

	defer resp.Body.Close()
	body, err = html.Parse(resp.Body)
	return
}

func searchLinks(elems []*html.Node, opt *crawlOpt) []string {
	links := []string{}
	containment := scrape.FindAll(opt.Search.ContainsTags...)
	for _, elem := range elems {
		if len(opt.Search.ContainsTags) > 0 && len(containment(elem)) == 0 {
			continue
		}
		for _, attr := range elem.Attr {
			if attr.Key == "href" {
				links = append(links, attr.Val)
			}
		}
	}
	return links
}

func resolveRef(base, ref string, SameHost bool) (link string, err error) {
	normalFlag := purell.FlagsUnsafeGreedy
	refURL, err := url.Parse(purell.MustNormalizeURLString(ref, normalFlag))
	if err != nil {
		return
	}
	baseURL, err := url.Parse(purell.MustNormalizeURLString(base, normalFlag))
	if err == nil {
		resURL := baseURL.ResolveReference(refURL)
		hostname := resURL.Hostname()
		if len(hostname) == 0 {
			err = fmt.Errorf("invalid uri: %s", link)
		} else if SameHost && hostname != baseURL.Hostname() {
			err = fmt.Errorf("external hostname: %s", hostname)
		} else {
			link = resURL.String()
		}
	}
	return
}
