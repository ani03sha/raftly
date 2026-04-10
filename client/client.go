package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
)


func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "Usage: raftly-client <addr> <command> [args...]")
		fmt.Fprintln(os.Stderr, "Commands:")
		fmt.Fprintln(os.Stderr, "  put <key> <value>")
		fmt.Fprintln(os.Stderr, "  get <key>")
		fmt.Fprintln(os.Stderr, "  delete <key>")
		fmt.Fprintln(os.Stderr, "  status")
		os.Exit(1)
	}

	addr := os.Args[1]
	cmd := os.Args[2]

	// Custom redirect policy: allow up to 10 redirects (default), but keep the original method.
    // Go already preserves method on 307/308 - this just documents intent.
	client := &http.Client{
		CheckRedirect: func(req *http.Request, via []*http.Request) (error) {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}

	switch cmd {
	case "put":
		if len(os.Args) < 5 {
			fmt.Fprintln(os.Stderr, "Usage: raftly-client <addr> put <key> <value>")
			os.Exit(1)
		}
		doPut(client, addr, os.Args[3], os.Args[4])
	case "get":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: raftly-client <addr> get <key>")
			os.Exit(1)
		}
		doGet(client, addr, os.Args[3])
	case "delete":
		if len(os.Args) < 4 {
			fmt.Fprintln(os.Stderr, "Usage: raftly-client <addr> delete <key>")
			os.Exit(1)
		}
		doDelete(client, addr, os.Args[3])
	case "status":
		doStatus(client, addr)
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", cmd)
		os.Exit(1)
	}
}


func doPut(client *http.Client, addr, key, value string) {
	body, _ := json.Marshal(map[string]string{"value": value})
	req, _ := http.NewRequest(http.MethodPut, "http://"+addr+"/keys/"+key, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("OK: %s = %s\n", key, value)
	} else {
		out, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error %d: %s\n", resp.StatusCode, out)
		os.Exit(1)
	}
}

func doGet(client *http.Client, addr, key string) {
	resp, err := client.Get("http://" + addr + "/keys/" + key)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	switch resp.StatusCode {
	case http.StatusOK:
		var result map[string]string
		json.NewDecoder(resp.Body).Decode(&result)
		fmt.Printf("%s = %s\n", result["key"], result["value"])
	case http.StatusNotFound:
		fmt.Printf("key %q not found\n", key)
	default:
		out, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error %d: %s\n", resp.StatusCode, out)
		os.Exit(1)
	}
}

func doDelete(client *http.Client, addr, key string) {
	req, _ := http.NewRequest(http.MethodDelete, "http://"+addr+"/keys/"+key, nil)
	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusOK {
		fmt.Printf("OK: deleted %s\n", key)
	} else {
		out, _ := io.ReadAll(resp.Body)
		fmt.Fprintf(os.Stderr, "error %d: %s\n", resp.StatusCode, out)
		os.Exit(1)
	}
}

func doStatus(client *http.Client, addr string) {
	resp, err := client.Get("http://" + addr + "/status")
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	var status map[string]interface{}
	json.NewDecoder(resp.Body).Decode(&status)
	for k, v := range status {
		fmt.Printf("%-15s %v\n", k+":", v)
	}
}