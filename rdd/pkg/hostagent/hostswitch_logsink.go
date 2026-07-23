// SPDX-License-Identifier: Apache-2.0
// SPDX-FileCopyrightText: SUSE LLC
// SPDX-FileCopyrightText: The Rancher Desktop Authors

package hostagent

import (
	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"github.com/sirupsen/logrus"
)

// logrusSink adapts logr to logrus for host-switch. funcr.New collapses Info
// and Error into a single callback that cannot tell them apart, so host-switch
// failures (bridge died, stalled data plane, unexpose errors) would all land at
// level=info. This sink mirrors funcr's own fnlogger but routes Info through
// logrus.Info and Error through logrus.Error, preserving the level distinction
// while keeping funcr.Formatter's verbosity gate.
type logrusSink struct {
	funcr.Formatter
}

// newLogrusLogger returns a logr.Logger, named name, that forwards to logrus
// with Info/Error levels preserved. verbosity gates V(n) Info calls.
func newLogrusLogger(name string, verbosity int) logr.Logger {
	sink := &logrusSink{
		Formatter: funcr.NewFormatter(funcr.Options{Verbosity: verbosity}),
	}
	sink.Formatter.AddName(name)
	return logr.New(sink)
}

func (l logrusSink) WithName(name string) logr.LogSink {
	l.Formatter.AddName(name)
	return &l
}

func (l logrusSink) WithValues(kvList ...any) logr.LogSink {
	l.Formatter.AddValues(kvList)
	return &l
}

func (l logrusSink) WithCallDepth(depth int) logr.LogSink {
	l.Formatter.AddCallDepth(depth)
	return &l
}

func (l logrusSink) Info(level int, msg string, kvList ...any) {
	prefix, args := l.FormatInfo(level, msg, kvList)
	if prefix != "" {
		logrus.Infof("%s: %s", prefix, args)
	} else {
		logrus.Info(args)
	}
}

func (l logrusSink) Error(err error, msg string, kvList ...any) {
	// FormatError already folds err into args as an "error" key.
	prefix, args := l.FormatError(err, msg, kvList)
	if prefix != "" {
		logrus.Errorf("%s: %s", prefix, args)
	} else {
		logrus.Error(args)
	}
}

// Assert conformance to the interfaces funcr's fnlogger implements.
var (
	_ logr.LogSink          = &logrusSink{}
	_ logr.CallDepthLogSink = &logrusSink{}
)
