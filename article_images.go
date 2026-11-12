package main

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	xhtml "golang.org/x/net/html"
)

func htmlAttribute(node *xhtml.Node, key string) string {
	for _, attribute := range node.Attr {
		if strings.EqualFold(attribute.Key, key) {
			return attribute.Val
		}
	}
	return ""
}

// Prefer the actual strip over social cards, which can be cropped thumbnails.
func discoverArticleImage(page *url.URL, document io.Reader) string {
	root, err := xhtml.Parse(io.LimitReader(document, maxFaviconPageBytes))
	if err != nil {
		return ""
	}
	var strip, social, twitter, lead string
	var visit func(*xhtml.Node, bool, bool)
	visit = func(node *xhtml.Node, inComic, inArticle bool) {
		if node.Type == xhtml.ElementNode {
			inArticle = inArticle || node.Data == "article" || node.Data == "main"
			id := htmlAttribute(node, "id")
			classes := strings.Fields(htmlAttribute(node, "class"))
			inComic = inComic || id == "comic" || id == "comic-image"
			for _, class := range classes {
				if class == "comicpage" || class == "webcomic-media" || class == "webcomic-image" || class == "widget_mgsisk_webcomic_collection_widget_webcomicmedia" {
					inComic = true
				}
			}
			if node.Data == "img" && strip == "" && (inComic || id == "comic" || id == "comicimage") {
				strip = htmlAttribute(node, "data-src")
				if strip == "" {
					strip = htmlAttribute(node, "src")
				}
			}
			if node.Data == "img" && inArticle && lead == "" {
				lead = htmlAttribute(node, "data-src")
				if lead == "" {
					lead = htmlAttribute(node, "src")
				}
			}
			if node.Data == "meta" && twitter == "" && (strings.EqualFold(htmlAttribute(node, "name"), "twitter:image") || strings.EqualFold(htmlAttribute(node, "name"), "twitter:image:src")) {
				twitter = htmlAttribute(node, "content")
			}
			if node.Data == "meta" && social == "" && strings.EqualFold(htmlAttribute(node, "property"), "og:image") {
				social = htmlAttribute(node, "content")
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			visit(child, inComic, inArticle)
		}
	}
	visit(root, false, false)
	for _, raw := range []string{strip, social, twitter, lead} {
		candidate, err := url.Parse(strings.TrimSpace(raw))
		if err != nil || raw == "" {
			continue
		}
		resolved := page.ResolveReference(candidate).String()
		if _, err := parseRemoteImageURL(resolved); err == nil {
			return resolved
		}
	}
	return ""
}

func (app *App) handleArticleImage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	user, ok := app.getSession(r)
	if !ok || user == nil {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	var link string
	err := app.db.QueryRow(`SELECT a.link FROM articles a JOIN feeds f ON f.id = a.feed_id
  WHERE a.id = ? AND f.created_by = ?`, r.URL.Query().Get("id"), user.ID).Scan(&link)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	page, err := parseRemoteImageURL(link)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, page.String(), nil)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	request.Header.Set("User-Agent", "feedss/1.0")
	request.Header.Set("Accept", "text/html")
	// Reuse the image proxy's public-address checks, including on redirects.
	client := imageProxyClient()
	defer client.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		http.Error(w, "article page unavailable", http.StatusBadGateway)
		return
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		http.Error(w, "article page unavailable", http.StatusBadGateway)
		return
	}
	imageURL := discoverArticleImage(response.Request.URL, response.Body)
	if imageURL == "" {
		http.NotFound(w, r)
		return
	}
	data, contentType, err := fetchRemoteImage(ctx, imageURL)
	if err != nil {
		http.Error(w, "article image unavailable", http.StatusBadGateway)
		return
	}
	// Cache image bytes, never the expiring URLs used by some comic sites.
	writeProxiedImage(w, data, contentType)
}
