package blog

import (
	"html/template"

	"net/http"

	"bytes"

	"time"

	"encoding/json"

	"os"

	"path/filepath"
	"sort"

	"strings"

	"log"

	"regexp"

	"fmt"

	"encoding/xml"

	"github.com/ryank90/utilities/blog/atom"
	"golang.org/x/tools/present"
)

var validJSONPFunc = regexp.MustCompile(`(?i)^[a-z_][a-z0-9_.]*$`)

// Config: specifies the server configuration values.

type Config struct {
	ContentPath  string // Path to the content files for the blog.
	TemplatePath string // Path to the template files for the blog.

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
	HTML      template.HTML // Rendered content.

	Related      []*Doc // Related content.
	Newer, Older *Doc   // Supporting newer and older content.
}

// Server: implements a http.handler that serves content.

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

// JsonItem: specifies a JSON item.

type jsonItem struct {
	Title   string
	Link    string
	Time    time.Time
	Summary string
	Content string
	Author  string
}

// RootData: encapsulates data destined for the root template.

type rootData struct {
	Doc      *Doc
	BasePath string
	Data     interface{}
}

// NewServer constructs a new server using the specified configuration.

func NewServer(cfg Config) (*Server, error) {
	root := filepath.Join(cfg.TemplatePath, "root.tmpl")
	parse := func(name string) (*template.Template, error) {
		t := template.New("").Funcs(funcMap)
		return t.ParseFiles(root, filepath.Join(cfg.TemplatePath, name))
	}

	s := &Server{cfg: cfg}

	// Parse templates.
	var err error
	s.template.home, err = parse("home.tmpl")
	if err != nil {
		return nil, err
	}
	s.template.index, err = parse("index.tmpl")
	if err != nil {
		return nil, err
	}
	s.template.article, err = parse("article.tmpl")
	if err != nil {
		return nil, err
	}
	s.template.page, err = parse("page.tmpl")
	if err != nil {
		return nil, err
	}
	p := present.Template().Funcs(funcMap)
	s.template.doc, err = p.ParseFiles(filepath.Join(cfg.TemplatePath, "doc.tmpl"))
	if err != nil {
		return nil, err
	}

	// Load content.
	err = s.loadDocs(filepath.Clean(cfg.ContentPath))
	if err != nil {
		return nil, err
	}

	err = s.renderAtomFeed()
	if err != nil {
		return nil, err
	}

	err = s.renderJSONFeed()
	if err != nil {
		return nil, err
	}

	// Set up content file server.
	s.content = http.StripPrefix(s.cfg.BasePath, http.FileServer(http.Dir(cfg.ContentPath)))

	return s, nil
}

// ServeHTTP servers the templates as well as the ATOM and JSON feeds.

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var (
		d = rootData{BasePath: s.cfg.BasePath}
		t *template.Template
	)
	switch p := strings.TrimPrefix(r.URL.Path, s.cfg.BasePath); p {
	case "/":
		d.Data = s.docs
		if len(s.docs) > s.cfg.HomeArticles {
			d.Data = s.docs[:s.cfg.HomeArticles]
		}
		t = s.template.home
	case "/index":
		d.Data = s.docs
		t = s.template.index
	case "/feed.atom", "/feeds/posts/default":
		w.Header().Set("Content-type", "application/atom+xml; charset=utf-8")
		w.Write(s.atomFeed)
		return
	case "/.json":
		if p := r.FormValue("jsonp"); validJSONPFunc.MatchString(p) {
			w.Header().Set("Content-type", "application/javascript; charset=utf-8")
			fmt.Fprintf(w, "%v(%s)", p, s.jsonFeed)
			return
		}
		w.Header().Set("Content-type", "application/json; charset=utf-8")
		w.Write(s.jsonFeed)
		return
	default:
		doc, ok := s.docPaths[p]
		if !ok {
			// Not a doc; try to just serve static content.
			s.content.ServeHTTP(w, r)
			return
		}
		d.Doc = doc
		t = s.template.article
	}
	err := t.ExecuteTemplate(w, "root", d)
	if err != nil {
		log.Println(err)
	}
}

// LoadDocs: reads all content for the provided file system root and renders all
// the content it finds.

func (s *Server) loadDocs(root string) error {
	// Read content into docs (article) field.
	const ext = ".article"

	fn := func(p string, info os.FileInfo, err error) error {
		if filepath.Ext(p) != ext {
			return nil
		}

		f, err := os.Open(p)

		if err != nil {
			return err
		}

		defer f.Close()

		d, err := present.Parse(f, p, 0)

		if err != nil {
			return err
		}

		html := new(bytes.Buffer)
		err = d.Render(html, s.template.doc)
		if err != nil {
			return err
		}

		p = p[len(root) : len(p)-len(ext)] // Trim root and extension.
		p = filepath.ToSlash(p)

		s.docs = append(s.docs, &Doc{
			Doc:       d,
			Path:      s.cfg.BasePath + p,
			Permalink: s.cfg.BaseURL + p,
			HTML:      template.HTML(html.String()),
		})

		return nil
	}

	err := filepath.Walk(root, fn)
	if err != nil {
		return err
	}

	sort.Sort(docsByTime(s.docs))

	// Pull out doc (article) paths and tags and put in reverse-associating maps.
	s.docPaths = make(map[string]*Doc)
	s.docTags = make(map[string][]*Doc)

	for _, d := range s.docs {
		s.docPaths[strings.TrimPrefix(d.Path, s.cfg.BasePath)] = d
		for _, t := range d.Tags {
			s.docTags[t] = append(s.docTags[t], d)
		}
	}

	// Pull out unique sorted list of tags.
	for t := range s.docTags {
		s.tags = append(s.tags, t)
	}

	sort.Strings(s.tags)

	// Setup presentation-related fields, Newer, Older, and Related.
	for _, doc := range s.docs {
		// Newer, Older: docs adjacent to Doc (Article).
		for i := range s.docs {
			if s.docs[i] != doc {
				continue
			}

			if i > 0 {
				doc.Newer = s.docs[i-1]
			}

			if i+1 < len(s.docs) {
				doc.Older = s.docs[i+1]
			}

			break
		}

		// Related: all docs (content) that share tags with doc.
		related := make(map[*Doc]bool)

		for _, t := range doc.Tags {
			for _, d := range s.docTags[t] {
				if d != doc {
					related[d] = true
				}
			}
		}

		for d := range related {
			doc.Related = append(doc.Related, d)
		}

		sort.Sort(docsByTime(doc.Related))
	}

	return nil
}

// RenderAtomFeed: generates an XML Atom feed and stores it in the Server's atomFeed field.

func (s *Server) renderAtomFeed() error {
	var updated time.Time

	if len(s.docs) > 0 {
		updated = s.docs[0].Time
	}

	feed := atom.Feed{
		Title:   s.cfg.FeedTitle,
		ID:      "tag:" + s.cfg.Hostname + ",2013:" + s.cfg.Hostname,
		Updated: atom.Time(updated),
		Link: []atom.Link{{
			Rel:  "self",
			Href: s.cfg.BaseURL + "/feed.atom",
		}},
	}

	for i, doc := range s.docs {
		if i >= s.cfg.FeedArticles {
			break
		}

		e := &atom.Entry{
			Title: doc.Title,
			ID:    feed.ID + doc.Path,
			Link: []atom.Link{{
				Rel:  "alternative",
				Href: doc.Permalink,
			}},
			Published: atom.Time(doc.Time),
			Updated:   atom.Time(doc.Time),
			Summary: &atom.Text{
				Type: "html",
				Body: summary(doc),
			},
			Content: &atom.Text{
				Type: "html",
				Body: string(doc.HTML),
			},
			Author: &atom.Person{
				Name: authors(doc.Authors),
			},
		}

		feed.Entry = append(feed.Entry, e)
	}

	data, err := xml.Marshal(&feed)
	if err != nil {
		return err
	}

	s.atomFeed = data
	return nil
}

// RenderJSONFeed: generates a JSON feed and stores it in the Server's jsonFeed field.

func (s *Server) renderJSONFeed() error {
	var feed []jsonItem

	for i, doc := range s.docs {
		if i >= s.cfg.FeedArticles {
			break
		}

		item := jsonItem{
			Title:   doc.Title,
			Link:    doc.Permalink,
			Time:    doc.Time,
			Summary: summary(doc),
			Content: string(doc.HTML),
			Author:  authors(doc.Authors),
		}

		feed = append(feed, item)
	}

	data, err := json.Marshal(feed)

	if err != nil {
		return err
	}

	s.jsonFeed = data
	return nil
}

var funcMap = template.FuncMap{
	"sectioned": sectioned,
	"authors":   authors,
}

// Sectioned: returns true if the Doc (Article) contains more than one section.

func sectioned(d *present.Doc) bool {
	return len(d.Sections) > 1
}

// Authors: returns a comma-separated list of author names.

func authors(authors []present.Author) string {
	var b bytes.Buffer

	last := len(authors) - 1

	for i, a := range authors {
		if i > 0 {
			if i == last {
				b.WriteString(" and ")
			} else {
				b.WriteString(", ")
			}
		}

		b.WriteString(authorName(a))
	}

	return b.String()
}

// AuthorName: returns the first line of the Author text: the authors name.

func authorName(a present.Author) string {
	el := a.TextElem()

	if len(el) == 0 {
		return ""
	}

	text, ok := el[0].(present.Text)

	if !ok || len(text.Lines) == 0 {
		return ""
	}

	return text.Lines[0]
}

// Summary: returns the first paragraph of text from the provided Doc (Article).

func summary(d *Doc) string {
	if len(d.Sections) == 0 {
		return ""
	}

	for _, elem := range d.Sections[0].Elem {
		text, ok := elem.(present.Text)
		if !ok || text.Pre {
			continue
		}

		var buf bytes.Buffer

		for _, s := range text.Lines {
			buf.WriteString(string(present.Style(s)))
			buf.WriteByte('\n')
		}

		return buf.String()
	}

	return ""
}

// DocsByTime implements sort.Interface, sorting Docs by their Time field.

type docsByTime []*Doc

func (s docsByTime) Len() int {
	return len(s)
}

func (s docsByTime) Swap(i, j int) {
	s[i], s[j] = s[j], s[i]
}

func (s docsByTime) Less(i, j int) bool {
	return s[i].Time.After(s[j].Time)
}
