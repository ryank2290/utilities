package blog

import (
	"html/template"

	"net/http"

	"golang.org/x/tools/present"
)

// Config: specifies the server configuration values.

type Config struct {
	ContentPath string // Path to the content files for the blog.
	ThemePath   string // Path to the theme files for the blog.

	BaseURL  string // Absolute base URL (for perm-links - no trailing slashes).
	BasePath string // Base URL path relative to server root - no trailing slashes.
	Hostname string // Server hostname used for rendering ATOM feeds.

	HomeArticles int    // Amount of Articles to display on the homepage.
	FeedArticles int    // Amount of Articles to display on the ATOM and JSON feeds.
	FeedTitle    string // The title of the ATOM XML feed
}

// Doc: specifies an article full of content.

type Doc struct {
	*present.Doc
	Permalink string        // Canonical URL for this document.
	Path      string        // Path relative to server root (including base).
	HTML      template.HTML // Rendered articles.

	Related      []*Doc // Related articles.
	Newer, Older *Doc   // Supporting newer and older articles.
}

// Server: implements a http.handler that serves articles.

type Server struct {
	cfg      Config          // Configuration.
	docs     []*Doc          // Articles.
	tags     []string        // Tags.
	docPaths map[string]*Doc // Key is path without the BasePath.
	docTags  map[string][]*Doc
	template struct {
		home, index, article, page, doc *template.Template
	}
	atomFeed []byte // Pre-rendered ATOM feed.
	jsonFeed []byte // Pre-rendered JSON feed.
	content  http.Handler
}

// NewServer constructs a new server using the specified configuration.

func NewServer(cfg Config) (*Server, error) {

}
