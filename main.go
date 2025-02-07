package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"
	_ "time/tzdata"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type regionFlag []string

func (r *regionFlag) String() string {
	return strings.Join(*r, ", ")
}

func (r *regionFlag) Set(value string) error {
	*r = append(*r, value)
	return nil
}

func main() {
	var regions regionFlag
	flag.Var(&regions, "region", "region to query for, SN1-4, can be passed multiple times")
	txtfile := flag.String("output.file", "", "write metrics to specified file (must have .prom extension)")
	addr := flag.String("output.http", "", "host:port to listen on for HTTP scrapes")
	showVersion := flag.Bool("version", false, "show version and build info")

	flag.Parse()

	if *showVersion {
		fmt.Fprintf(os.Stdout, "{\"version\": \"%s\", \"commit\": \"%s\", \"date\": \"%s\"}\n", Version(), Commit(), Timestamp())
		os.Exit(0)
	}

	if len(regions) == 0 {
		log.Fatalln("need at least one region")
	}

	loc, err := time.LoadLocation("Europe/Helsinki")
	if err != nil {
		log.Fatalln(err)
	}

	c := prometheus.NewPedanticRegistry()
	c.MustRegister(NewVattenfallCollector(regions, loc))

	if *txtfile == "" && *addr == "" {
		WriteMetricsTo(os.Stdout, c)
		os.Exit(0)
	}

	if *txtfile != "" {
		if elems := strings.Split(*txtfile, "."); elems[len(elems)-1] != "prom" {
			log.Fatalln("filename must end with .prom extension:", *txtfile)
		}
		err := prometheus.WriteToTextfile(*txtfile, c)
		if err != nil {
			log.Fatalln(err)
		}
		os.Exit(0)
	}

	if *addr != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()

		p := prometheus.NewPedanticRegistry()
		p.MustRegister(collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
		p.MustRegister(collectors.NewGoCollector())

		listener, err := net.Listen("tcp", *addr)
		if err != nil {
			log.Fatalln(err)
		}

		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.HandlerFor(p, promhttp.HandlerOpts{}))
		mux.Handle("/prices", promhttp.InstrumentMetricHandler(p, promhttp.HandlerFor(c, promhttp.HandlerOpts{})))
		mux.Handle("/forecast", promhttp.InstrumentMetricHandler(p, http.HandlerFunc(forecastHandler(loc, regions))))
		h := &http.Server{
			Handler: mux,
		}
		go func() {
			if err := h.Serve(listener); err != http.ErrServerClosed {
				log.Fatalln(err.Error())
			}
		}()
		log.Println("exporter listening on:", listener.Addr().String())

		<-ctx.Done()

		t, tc := context.WithTimeout(context.Background(), 5*time.Second)
		defer tc()
		h.Shutdown(t)

		log.Println("exporter shutdown completed")
	}
}
