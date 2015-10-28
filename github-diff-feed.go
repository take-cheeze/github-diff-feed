package main

import (
	"bytes"
	"encoding/xml"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"os"
	"regexp"
	"sort"
	"time"
	"golang.org/x/tools/blog/atom"
	"github.com/gorilla/feeds"
	"github.com/shurcooL/highlight_diff"
	"github.com/gin-gonic/gin"
)

type FeedItem struct {
	Url string
	Updated time.Time
	Patch string
	Title string
	Author string
}

type FeedItems []*FeedItem

const FEED_ITEM_MAX = 100

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
			if err != nil { log.Fatalf("feed fetch error: %s", err) }

			body, err := ioutil.ReadAll(resp.Body)
			if err != nil { log.Fatalf("failed reading feed body: %s", err) }

			d := xml.NewDecoder(bytes.NewReader(body))
			a := atom.Feed {}
			dec_err := d.Decode(&a)
			if dec_err != nil { log.Fatalf("failed to parse feed: %s", dec_err) }

			for _, e := range a.Entry { patch_chan <- e }

			<- ticker.C
		}
	}()

	URL_MATCH := regexp.MustCompile(`^https://github.com/([\w\-_]+)/([\w\-_]+)/compare/(\w+)\.\.\.(\w+)$`)

	go func() {
		for {
			e := <-patch_chan
			link := e.Link[0].Href

			log.Printf("Checking: %s", link)

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

			parsed_time, time_err := time.Parse("2006-01-02T15:04:05Z", string(e.Updated))
			if time_err != nil { log.Fatalf("failed parsing time: %s", time_err) }

			item := FeedItem{
				Url: e.Link[0].Href, Updated: parsed_time, Author: e.Author.Name,
				Title: fmt.Sprintf("%s (%s...%s)", e.Title, m[3], m[4]) }

			log.Printf("Fetching: %s.patch", item.Url)

			resp, err := http.Get(item.Url + ".patch")
			if err != nil { log.Fatalf("patch fetch error: %s", err) }

			src, err := ioutil.ReadAll(resp.Body)
			if err != nil { log.Fatalf("failed reading feed body: %s", err) }

			var buf bytes.Buffer
			hl_err := highlight_diff.Print(highlight_diff.NewScanner(src), &buf)
			if hl_err != nil { log.Fatalf("Failed highlighting patch: %s", hl_err) }
			item.Patch = string(buf.Bytes())

			feed_items = append(feed_items, &item)
			feed_items.RemoveOld()
		}
	}()

	r := gin.Default()
	r.GET("/", func(c *gin.Context) {
		now := time.Now()
		feed := &feeds.Feed{
			Title: "feed generated from github feed",
			Link: &feeds.Link{Href: os.Getenv("HEROKU_URL")},
			Description: "feed generated from github feed",
			Author: &feeds.Author { "take-cheeze", "takechi101010@gmail.com" },
			Created: now,
		}

		feed.Items = make([]*feeds.Item, len(feed_items))
		for idx, i := range feed_items {
			feed.Items[idx] = &feeds.Item{
				Title: i.Title, Link: &feeds.Link{Href: i.Url}, Description: i.Patch,
				Author: &feeds.Author{"i.author", ""},
				Created: i.Updated,
			}
		}

		body, err := feed.ToAtom()
		if err != nil { log.Fatalf("failed generating atom feed: %s", err) }
		c.Data(200, "application/atom+xml", []byte(body))
	})
	r.Run(":" + port);
}
