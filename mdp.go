package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"io/ioutil"
	"log"
	"net/http"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
	"nhooyr.io/websocket"
)

// this is awful because github.com/fsnotify/fsnotify is awful
func watchFile(ctx context.Context, path string) (<-chan struct{}, error) {
	n, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	if err := n.Add(path); err != nil {
		return nil, err
	}
	c := make(chan struct{}, 1)
	go func() {
		defer close(c)
		defer n.Close()
		for {
			c <- struct{}{}
			select {
			case <-ctx.Done():
				return
			case event, ok := <-n.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Remove == fsnotify.Remove {
					if err := n.Add(path); err != nil {
						log.Print(err)
						return
					}
				}
				t := time.After(time.Millisecond * 50)
				// eat successive events
			loop:
				for {
					select {
					case <-t:
						break loop
					case event, ok := <-n.Events:
						if !ok {
							return
						}
						if event.Op&fsnotify.Remove == fsnotify.Remove {
							if err := n.Add(path); err != nil {
								log.Print(err)
								return
							}
						}
					}
				}
			case err, ok := <-n.Errors:
				if !ok {
					return
				}
				log.Print(err)
			}
		}
	}()
	return c, nil
}

func ws(w http.ResponseWriter, r *http.Request, path string) error {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return err
	}
	defer c.Close(websocket.StatusInternalError, "")
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute*10)
	defer cancel()
	ctx = c.CloseRead(r.Context())
	n, err := watchFile(ctx, path)
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
			putFile(ctx, c, path)
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
	gm := goldmark.New(goldmark.WithExtensions(extension.GFM))
	if err := gm.Convert(src, w); err != nil {
		log.Print(err)
	}
	return nil
}

func main() {
	addr := flag.String("addr", "localhost:6969", "address to listen on")
	flag.Parse()
	args := flag.Args()
	if len(args) != 1 {
		log.Fatal("too many arguments")
	}
	path := args[0]
	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.ServeContent(w, r, "index.html", time.Now(), bytes.NewReader(html))
	})
	http.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		err := ws(w, r, path)
		if err != nil {
			log.Print(err)
		}
	})
	log.Printf("http://%s", *addr)
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
    const sock = new WebSocket("ws://" + window.location.host + "/ws");
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
