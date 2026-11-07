// Command balloon drives the drawing-ballooning pipeline from the terminal.
//
// It exists mainly so the engine can be exercised, demonstrated and debugged
// without the browser frontend in the way.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/api"
	"github.com/monika1991123/engineering-drawing-balloon-editor/web"

	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/demo"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/dimension"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/model"
	"github.com/monika1991123/engineering-drawing-balloon-editor/internal/render"
)

const usage = `balloon - engineering drawing ballooning and inspection reports

usage:
  balloon parse <callout>...      parse drawing callouts and print them as JSON
  balloon demo [-o out.svg]       render a synthetic ballooned drawing
  balloon build -i texts.json [-o out.svg]
                                  run the pipeline over extracted PDF text
  balloon serve [-addr :8080]     open the drawing editor in a browser

examples:
  balloon parse '4X ⌀12.50 ±0.05' '25 H7' '⌖|⌀0.2Ⓜ|A|B|C'
  balloon demo -o demo.svg
  balloon serve
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}

	var err error
	switch os.Args[1] {
	case "parse":
		err = cmdParse(os.Args[2:])
	case "demo":
		err = cmdDemo(os.Args[2:])
	case "build":
		err = cmdBuild(os.Args[2:])
	case "serve":
		err = cmdServe(os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "balloon: unknown command %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}

	if err != nil {
		fmt.Fprintln(os.Stderr, "balloon:", err)
		os.Exit(1)
	}
}

func cmdParse(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("parse needs at least one callout")
	}
	opt := dimension.DefaultOptions()
	out := make([]dimension.Characteristic, 0, len(args))
	for _, a := range args {
		out = append(out, dimension.Parse(a, opt))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

func cmdDemo(args []string) error {
	fs := flag.NewFlagSet("demo", flag.ExitOnError)
	outPath := fs.String("o", "demo.svg", "output SVG path")
	debug := fs.Bool("debug", false, "draw text boxes and obstacle regions")
	if err := fs.Parse(args); err != nil {
		return err
	}

	d, texts := demo.Drawing()
	model.Build(d, texts)

	f, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	st := render.DefaultStyle()
	st.ShowTextBoxes = *debug
	st.ShowObstacles = *debug
	if err := render.SVG(f, d, 0, st); err != nil {
		return err
	}

	report(d, *outPath)
	return nil
}

func cmdBuild(args []string) error {
	fs := flag.NewFlagSet("build", flag.ExitOnError)
	in := fs.String("i", "", "JSON file of extracted text items (required)")
	outPath := fs.String("o", "out.svg", "output SVG path")
	debug := fs.Bool("debug", false, "draw text boxes and obstacle regions")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *in == "" {
		return fmt.Errorf("build needs -i")
	}

	raw, err := os.ReadFile(*in)
	if err != nil {
		return err
	}
	var payload struct {
		Drawing model.Drawing    `json:"drawing"`
		Texts   []model.TextItem `json:"texts"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return fmt.Errorf("parsing %s: %w", *in, err)
	}

	d := &payload.Drawing
	model.Build(d, payload.Texts)

	f, err := os.Create(*outPath)
	if err != nil {
		return err
	}
	defer f.Close()

	st := render.DefaultStyle()
	st.ShowTextBoxes = *debug
	st.ShowObstacles = *debug
	if err := render.SVG(f, d, 0, st); err != nil {
		return err
	}

	report(d, *outPath)
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "listen address")
	if err := fs.Parse(args); err != nil {
		return err
	}

	srv := &http.Server{
		Addr:              *addr,
		Handler:           api.New(web.FS),
		ReadHeaderTimeout: 10 * time.Second,
	}
	urlAddr := *addr
	if strings.HasPrefix(urlAddr, ":") {
		urlAddr = "localhost" + urlAddr
	}
	fmt.Fprintf(os.Stderr, "balloon: http://%s\n", urlAddr)
	return srv.ListenAndServe()
}

// report prints a short summary to stderr so the command is useful when its
// stdout is being redirected.
func report(d *model.Drawing, outPath string) {
	inspectable := len(d.Inspectable())
	unclean := 0
	for _, it := range d.Items {
		if !it.Clean {
			unclean++
		}
	}

	fmt.Fprintf(os.Stderr, "%s: %d characteristics, %d inspectable, %d need attention\n",
		outPath, len(d.Items), inspectable, unclean)

	if w := d.Warnings(); len(w) > 0 {
		fmt.Fprintln(os.Stderr, "\nwarnings:")
		for _, s := range w {
			fmt.Fprintln(os.Stderr, "  -", s)
		}
	}
}
