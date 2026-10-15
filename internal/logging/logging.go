package logging

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"strconv"

	"github.com/go-logr/logr"
	"k8s.io/klog/v2"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
)

type Options struct {
	format       string
	level        slog.Level
	levelSet     bool
	verbositySet bool
}

func BindFlags(fs *flag.FlagSet) *Options {
	opts := &Options{}
	fs.StringVar(&opts.format, "log-format", "json", "Log format: json or text.")
	fs.Func("log-level", "Log level: info, debug (Logr V(4)), or error (default info). Mutually exclusive with log-verbosity.", func(value string) error {
		switch value {
		case "info":
			opts.level = slog.LevelInfo
		case "debug":
			opts.level = slog.LevelDebug
		case "error":
			opts.level = slog.LevelError
		default:
			return fmt.Errorf("invalid log level %q: expected info, debug, or error", value)
		}
		opts.levelSet = true
		return nil
	})
	fs.Func("log-verbosity", "Logr verbosity threshold (0 to 2147483647). Mutually exclusive with log-level.", func(value string) error {
		verbosity, err := strconv.ParseInt(value, 10, 32)
		if err != nil || verbosity < 0 {
			return fmt.Errorf("invalid log verbosity %q: expected an integer from 0 to 2147483647", value)
		}
		opts.level = -slog.Level(verbosity)
		opts.verbositySet = true
		return nil
	})
	return opts
}

func (o *Options) NewHandler(writer io.Writer) (slog.Handler, error) {
	if o.levelSet && o.verbositySet {
		return nil, fmt.Errorf("log-level and log-verbosity are mutually exclusive")
	}
	opts := &slog.HandlerOptions{Level: o.level}
	switch o.format {
	case "json":
		return slog.NewJSONHandler(writer, opts), nil
	case "text":
		return slog.NewTextHandler(writer, opts), nil
	default:
		return nil, fmt.Errorf("invalid log format %q: expected json or text", o.format)
	}
}

func (o *Options) Install(writer io.Writer) error {
	handler, err := o.NewHandler(writer)
	if err != nil {
		return err
	}
	fs := flag.NewFlagSet("klog", flag.ContinueOnError)
	klog.InitFlags(fs)
	if err := fs.Set("v", strconv.Itoa(max(0, -int(o.level)))); err != nil {
		return fmt.Errorf("configure klog verbosity: %w", err)
	}
	logger := slog.New(handler)
	logf.SetLogger(logr.FromSlogHandler(handler))
	slog.SetDefault(logger)
	klog.SetSlogLogger(logger)
	return nil
}
