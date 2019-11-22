package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	"nhooyr.io/websocket"
)

// this is awful because github.com/fsnotify/fsnotify is awful
func watchFile(ctx context.Context, p string) (<-chan struct{}, error) {
	n, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	c := make(chan struct{}, 1)
	go func() {
		defer close(c)
		defer n.Close()
		for {
			c <- struct{}{}
			if err := n.Add(p); err != nil {
				log.Print(err)
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-n.Events:
				t := time.After(time.Millisecond * 50)
			loop:
				for {
					select {
					case <-t:
						break loop
					case <-n.Events:
					}
				}
			case err := <-n.Errors:
				log.Print(err)
				return
			}
		}
	}()
	return c, nil
}

func ws(w http.ResponseWriter, r *http.Request, root, p string) error {
	if filepath.Separator != '/' && strings.ContainsRune(p, filepath.Separator) {
		return errors.New("invalid character in file path")
	}
	p = filepath.Join(root, filepath.FromSlash(path.Clean("/"+p)))
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusInternalError, "")
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute*10)
	defer cancel()
	ctx = c.CloseRead(r.Context())
	n, err := watchFile(ctx, p)
	if err != nil {
		return err
	}
	for {
		select {
		case <-ctx.Done():
			c.Close(websocket.StatusNormalClosure, "")
			return nil
		case _, ok := <-n:
			if !ok {
				return errors.New("unexpected error")
			}
			putFile(ctx, c, p)
		}
	}
}

func putFile(ctx context.Context, c *websocket.Conn, path string) error {
	src, err := ioutil.ReadFile(path)
	if err != nil {
		log.Fatal(err)
	}
	w, err := c.Writer(ctx, websocket.MessageText)
	if err != nil {
		return err
	}
	defer w.Close()
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM),
		goldmark.WithParserOptions(parser.WithAutoHeadingID()))
	if err := gm.Convert(src, w); err != nil {
		log.Print(err)
	}
	return nil
}

func main() {
	addr := flag.String("addr", "localhost:6969", "address to listen on")
	flag.Parse()
	args := flag.Args()
	if len(args) > 1 {
		log.Fatal("too many arguments")
	}
	root := "."
	if len(args) == 1 {
		root = args[0]
	}
	p := ""
	if path.Ext(root) == ".md" {
		p = "/" + path.Clean(filepath.Base(root))
		root = filepath.Dir(root)
	}
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		rp := path.Clean(r.URL.Path)
		switch {
		case rp == "/" && p != "":
			http.Redirect(w, r, p, http.StatusFound)
		case path.Ext(rp) == ".md":
			http.ServeContent(w, r, "", time.Now(), bytes.NewReader(html))
		case path.Base(rp) == "ws" && path.Ext(path.Dir(rp)) == ".md":
			err := ws(w, r, root, path.Dir(rp))
			if err != nil {
				log.Print(err)
			}
		default:
			http.FileServer(http.Dir(filepath.Dir(root))).ServeHTTP(w, r)
		}
	})
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
	})
	fmt.Printf("http://%s\n", *addr)
	log.Fatal(http.ListenAndServe(*addr, nil))
}

var html = []byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <title>preview</title>
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <link rel="stylesheet" href="https://cdnjs.cloudflare.com/ajax/libs/github-markdown-css/3.0.1/github-markdown.min.css" integrity="sha256-HbgiGHMLxHZ3kkAiixyvnaaZFNjNWLYKD/QG6PWaQPc=" crossorigin="anonymous" />
  <style>
    .markdown-body {
      box-sizing: border-box;
      min-width: 200px;
      max-width: 980px;
      margin: 0 auto;
      padding: 45px;
    }
    @media (max-width: 767px) {
      .markdown-body {
        padding: 15px;
      }
    }
  </style>
</head>
<body>
  <script>
    const loc = window.location;
    const sock = new WebSocket("ws://" + loc.host + loc.pathname + "/ws");
    sock.addEventListener('message', function (event) { 
      document.getElementById("content").innerHTML = event.data;
    });
    sock.addEventListener('error', function (event) { console.log(event); })
    sock.addEventListener('close', function (event) { console.log(event); })
  </script>
  <article id="content" class="markdown-body">
  </article>
</body>
</html>`)
