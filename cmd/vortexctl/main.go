// Command vortexctl is an admin CLI for VORTEX, talking to its admin API
// over HTTP.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
)

func main() {
	addr := flag.String("admin-addr", "http://localhost:9090", "VORTEX admin API base address")
	flag.Parse()
	args := flag.Args()

	if len(args) == 0 {
		usage()
		os.Exit(2)
	}

	c := &client{base: strings.TrimRight(*addr, "/"), http: &http.Client{}}

	var err error
	switch args[0] {
	case "status":
		err = c.status()
	case "stats":
		err = c.stats()
	case "reload":
		err = c.reload()
	case "backend":
		err = c.backendCmd(args[1:])
	default:
		usage()
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "vortexctl:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, `usage: vortexctl [-admin-addr URL] <command> [args]

commands:
  status                     check VORTEX health
  stats                      show runtime statistics
  reload                     reload configuration
  backend list                list all backends
  backend show <name>         show one backend
  backend enable <name>       bring a backend back UP
  backend disable <name>      take a backend DOWN immediately
  backend drain <name>        drain a backend (finish traffic, then DOWN)`)
}

type client struct {
	base string
	http *http.Client
}

func (c *client) get(path string) ([]byte, int, error) {
	resp, err := c.http.Get(c.base + path)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return body, resp.StatusCode, nil
}

func (c *client) post(path string) ([]byte, int, error) {
	resp, err := c.http.Post(c.base+path, "application/json", nil)
	if err != nil {
		return nil, 0, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, 0, err
	}
	return body, resp.StatusCode, nil
}

func (c *client) status() error {
	body, code, err := c.get("/health")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("unhealthy (status %d): %s", code, body)
	}
	fmt.Println("OK")
	return nil
}

func (c *client) stats() error {
	body, code, err := c.get("/admin/stats")
	if err != nil {
		return err
	}
	return printJSON(body, code)
}

func (c *client) reload() error {
	body, code, err := c.post("/admin/reload")
	if err != nil {
		return err
	}
	if code != http.StatusOK {
		return fmt.Errorf("reload failed (status %d): %s", code, body)
	}
	fmt.Println("reloaded")
	return nil
}

func (c *client) backendCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: vortexctl backend <list|show|enable|disable|drain> [name]")
	}

	switch args[0] {
	case "list":
		body, code, err := c.get("/admin/backends")
		if err != nil {
			return err
		}
		return printJSON(body, code)

	case "show":
		if len(args) < 2 {
			return fmt.Errorf("usage: vortexctl backend show <name>")
		}
		body, code, err := c.get("/admin/backends/" + url.PathEscape(args[1]))
		if err != nil {
			return err
		}
		if code == http.StatusNotFound {
			return fmt.Errorf("backend %q not found", args[1])
		}
		return printJSON(body, code)

	case "enable", "disable", "drain":
		if len(args) < 2 {
			return fmt.Errorf("usage: vortexctl backend %s <name>", args[0])
		}
		body, code, err := c.post("/admin/backends/" + url.PathEscape(args[1]) + "/" + args[0])
		if err != nil {
			return err
		}
		if code == http.StatusNotFound {
			return fmt.Errorf("backend %q not found", args[1])
		}
		if code != http.StatusOK {
			return fmt.Errorf("%s failed (status %d): %s", args[0], code, body)
		}
		fmt.Printf("%s: %s\n", args[1], args[0])
		return nil

	default:
		return fmt.Errorf("unknown backend subcommand %q", args[0])
	}
}

func printJSON(body []byte, code int) error {
	if code != http.StatusOK {
		return fmt.Errorf("status %d: %s", code, body)
	}
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		fmt.Println(string(body))
		return nil
	}
	pretty, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	fmt.Println(string(pretty))
	return nil
}
