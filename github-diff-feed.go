package main

import (
	"encoding/xml"
	"fmt"
	"html"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"
	"golang.org/x/tools/blog/atom"
	"github.com/dustin/go-humanize"
	"github.com/gorilla/feeds"
	"github.com/gin-gonic/gin"
	"github.com/gin-contrib/gzip"
)

type FeedItem struct {
	Url string
	Updated time.Time
	Patch string
	Diff string
	Title string
	Author string
}

type FeedItems []*FeedItem

const FEED_ITEM_MAX = 50
const FEED_SIZE_THRESHOLD = 1 * 1024 * 1024 // 1 MB

func (s FeedItems) Len() int { return len(s) }
func (s FeedItems) Less(i, j int) bool { return s[i].Updated.Before(s[j].Updated) }
func (s FeedItems) Swap(i, j int) { s[i], s[j] = s[j], s[i] }
func (s FeedItems) RemoveOld() FeedItems {
	sort.Sort(s)
	l := 0
	if FEED_ITEM_MAX < len(s) { l = FEED_ITEM_MAX } else { l = len(s) }
	return s[len(s) - l:l]
}

func main() {
	port := os.Getenv("PORT")

	if port == "" { log.Fatal("$PORT must be set") }

	feed_items := FeedItems {}
	patch_chan := make(chan *atom.Entry)

	ticker := time.NewTicker(time.Minute * 5)
	go func() {
		for {
			feed_url := os.Getenv("GITHUB_FEED_URL")
			log.Printf("Fetching: %s", feed_url)
			resp, err := http.Get(feed_url)
			if err != nil {
				log.Printf("feed fetch error: %s", err)
				continue
			}

			d := xml.NewDecoder(resp.Body)
			a := atom.Feed {}
			err = d.Decode(&a)
			if err != nil {
				log.Printf("failed to parse feed: %s", err)
				continue
			}

			for _, e := range a.Entry { patch_chan <- e }

			<- ticker.C
		}
	}()

	URL_MATCH := regexp.MustCompile(`^https://github.com/([\w\-_]+)/([\w\-_]+)/compare/(\w+)\.\.\.(\w+)$`)
	// gh_client := github.NewClient(nil)
	// md_opt := &github.MarkdownOptions{Mode: "markdown"}

	go func() {
		for {
			e := <-patch_chan

			// skip github pages update
			if strings.Contains(e.Title, "pushed to gh-pages at") { continue }

			link := e.Link[0].Href

			// log.Printf("Checking: %s", link)

			already_fetched := false
			for _, v := range feed_items {
				if v.Url == link {
					already_fetched = true
					break
				}
			}
			if already_fetched { continue }

			m := URL_MATCH.FindStringSubmatch(link)
			if m == nil { continue }

			parsed_time, err := time.Parse("2006-01-02T15:04:05Z", string(e.Updated))
			if err != nil {
				log.Printf("failed parsing time: %s", err)
				continue
			}

			item := FeedItem{
				Url: e.Link[0].Href, Updated: parsed_time, Author: e.Author.Name,
				Title: fmt.Sprintf("%s (%s...%s)", e.Title, m[3], m[4]) }

			fetchUrl := func(url string) string {
				log.Printf("Fetching: %s", url)
				resp, err := http.Get(url)
				if resp.StatusCode != http.StatusOK {
					log.Printf("cannot access to data: %s", url)
					return ""
				}
				if err != nil {
					log.Printf("data fetch error: %s", err)
					return ""
				}
				src, err := ioutil.ReadAll(resp.Body)
				if err != nil {
					log.Printf("failed reading feed body: %s", err)
					return ""
				}

				var ret string
				if len(src) == 0 {
					return "" // skip empty feed
				} else if len(src) > FEED_SIZE_THRESHOLD {
					ret = fmt.Sprintf("Data size too big: %s", humanize.Bytes(uint64(len(src))))
				} else {
					ret = "<pre>" + html.EscapeString(string(src)) + "</pre>"
				}
				return ret;
			}

			item.Patch = fetchUrl(item.Url + ".patch")
			if len(item.Patch) == 0 {
				continue
			}
			item.Diff = fetchUrl(item.Url + ".diff")

			feed_items = append(feed_items, &item)
			feed_items.RemoveOld()
		}
	}()

	ping_ticker := time.NewTicker(time.Minute * 15)
	go func() {
		for {
			<- ping_ticker.C
			_, err := http.Get(os.Getenv("HEROKU_URL") + "ping")
			if err != nil {
				log.Printf("failed pinging to avoid idle: %s", err)
				continue
			}
		}
	}()

	r := gin.Default()
	r.Use(gzip.Gzip(gzip.DefaultCompression))
	getter := func(c *gin.Context, itemGetter func(*FeedItem) string) {
		now := time.Now()
		feed := &feeds.Feed{
			Title: "github-diff-feed",
			Link: &feeds.Link{Href: os.Getenv("HEROKU_URL")},
			Description: "feed generated from github feed",
			Author: &feeds.Author { "take-cheeze", "takechi101010@gmail.com" },
			Created: now,
		}

		feed.Items = make([]*feeds.Item, len(feed_items))
		for idx, i := range feed_items {
			feed.Items[idx] = &feeds.Item{
				Title: i.Title, Link: &feeds.Link{Href: i.Url}, Description: i.Patch,
				Author: &feeds.Author{i.Author, ""},
				Created: i.Updated,
			}
		}

		body, err := feed.ToAtom()
		if err != nil {
			log.Printf("failed generating atom feed: %s", err)
			c.Data(503, "text/plain", []byte("failed feed generation"))
			return
		}
		c.Data(200, "application/atom+xml", []byte(body))
	}
	r.GET("/", func(c *gin.Context) {
		getter(c, func(i *FeedItem) string {
			return i.Patch
		})
	})
	r.GET("/patch", func(c *gin.Context) {
		getter(c, func(i *FeedItem) string {
			return i.Patch
		})
	})
	r.GET("/diff", func(c *gin.Context) {
		getter(c, func(i *FeedItem) string {
			return i.Diff
		})
	})
	r.GET("/ping", func(c *gin.Context) {
		c.Data(200, "text/plain", []byte("pong"))
	})
	r.Run(":" + port);
}
