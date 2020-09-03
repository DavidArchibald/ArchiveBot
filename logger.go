package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	rotatelogs "github.com/lestrrat-go/file-rotatelogs"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewLogger .
func NewLogger(isProduction bool) (*zap.SugaredLogger, error) {
	var (
		config zap.Config
		err    error
	)
	if isProduction {
		config = zap.NewDevelopmentConfig()
	} else {
		config = zap.NewProductionConfig()
		config.Encoding = "console"
		config.EncoderConfig = zap.NewDevelopmentEncoderConfig()
	}

	if err != nil {
		return nil, err
	}

	logFile := "./logs/ArchiveBot-%Y-%m-%d-%H.log"
	rotator, err := rotatelogs.New(
		logFile,
		rotatelogs.WithMaxAge(60*24*time.Hour),
		rotatelogs.WithRotationTime(time.Hour),
	)
	if err != nil {
		return nil, err
	}
	w := zapcore.AddSync(rotator)
	fileCore := zapcore.NewCore(
		zapcore.NewConsoleEncoder(config.EncoderConfig),
		w,
		config.Level,
	)

	toFile := zap.WrapCore(func(c zapcore.Core) zapcore.Core {
		return zapcore.NewTee(c, fileCore)
	})

	logger, err := config.Build(toFile)
	if err != nil {
		return nil, err
	}

	return logger.Sugar(), nil
}

// log an error with added context if relevant.
func (c *Client) log(err error) {
	ce, ok := err.(*ContextError)
	if ok {
		ce.LogError(c.Logger)
	} else {
		c.Logger.Error(err)
	}
}

// fatal is equivalent to log() followed by os.Exit(1)
func (c *Client) fatal(err error) {
	if ce, ok := err.(*ContextError); ok {
		err = ce.Wrap("FATAL")
	} else {
		err = fmt.Errorf("FATAL: %w", err)
	}
	c.log(err)
	os.Exit(1)
}

// dfatal is fatal except it only exits in development mode.
// This is used more often that might be expected as to keep the processes running.
// Secondly, because there are many APIs that don't always behave consistently, issues may crop up without programmer error.
func (c *Client) dfatal(err error) {
	if ce, ok := err.(*ContextError); ok {
		err = ce.Wrap("DEVELOPMENT FATAL")
	} else {
		err = fmt.Errorf("DEVELOPMENT FATAL: %w", err)
	}
	c.log(err)
	if !c.IsProduction {
		os.Exit(1)
	}
}

// dpanic panics only in development mode.
func (c *Client) dpanic(v interface{}) {
	if c.IsProduction {
		panic(v)
	} else {
		c.Logger.Error("UNEXPECTED PANIC: %v", v)
	}
}

// ContextError is an error with additional debugging context.
type ContextError struct {
	context     []ContextParam
	contextKeys map[string]struct{}
	history     []ContextError
	err         error
}

// ContextParam is a contextual parameter.
type ContextParam struct {
	key   string
	value string
}

var _ error = &ContextError{}

// NewContextlessError creates a ContextError with no context. This is essentially a no-op.
func NewContextlessError(err error) *ContextError {
	return &ContextError{[]ContextParam{}, make(map[string]struct{}), nil, err}
}

// NewWrappedError wraps the error and then adds context.
func NewWrappedError(wrap string, err error, context []ContextParam) *ContextError {
	ce := NewContextError(err, context)
	ce.err = fmt.Errorf("%s: %w", wrap, err)

	return ce
}

// NewContextError creates a new contextual error.
func NewContextError(err error, context []ContextParam) *ContextError {
	if ce, ok := err.(*ContextError); ok {
		if ce == nil {
			return &ContextError{context, make(map[string]struct{}), nil, err}
		}

		temp := *ce
		for _, param := range context {
			if _, ok := ce.contextKeys[param.key]; ok {
				isSet := false
				// Replace an existing instance of context if one exists. Remove extras.
				for i, contextParam := range ce.context {
					if contextParam.key == param.key {
						if !isSet {
							temp.context[i] = param
						} else {
							temp.context = append(temp.context[:i], temp.context[i+1:]...)
						}
					}
				}

				if isSet {
					continue
				}
			}

			temp.context = append(temp.context, param)
			temp.contextKeys[param.key] = struct{}{}
		}

		temp.history = nil
		temp.history = append(ce.history, temp)

		return &temp
	}

	contextKeys := make(map[string]struct{})
	for _, param := range context {
		contextKeys[param.key] = struct{}{}
	}

	return &ContextError{context, contextKeys, nil, err}
}

// AddContext to an error.
func (ce *ContextError) AddContext(key, value string) *ContextError {
	ce.context = append(ce.context, ContextParam{key, value})
	ce.contextKeys[key] = struct{}{}
	return ce
}

// GetContext gets a contextual value.
func (ce *ContextError) GetContext(key string) string {
	for _, v := range ce.context {
		if v.key == key {
			return v.value
		}
	}

	return ""
}

// RemoveContext removes a contextual value.
func (ce *ContextError) RemoveContext(key string) *ContextError {
	if _, ok := ce.contextKeys[key]; !ok {
		return ce
	}

	for i, v := range ce.context {
		if v.key == key {
			ce.context = append(ce.context[:i], ce.context[i+1:]...)
		}
	}

	delete(ce.contextKeys, key)

	return ce
}

// Wrap this error, returning itself for chaining.
func (ce *ContextError) Wrap(msg string) *ContextError {
	history := make([]ContextError, len(ce.history))
	copy(ce.history, history)

	ce.history = nil
	ce.err = fmt.Errorf("%s: %w", msg, ce.err)
	ce.history = append(history, *ce)
	return ce
}

func (ce *ContextError) Error() string {
	return fmt.Sprint(ce.err)
}

func (ce *ContextError) Unwrap() error {
	if len(ce.history) != 0 {
		return ce.UnwrapContext()
	}

	return ce.err
}

// UnwrapContext unwraps a contextual error only.
func (ce *ContextError) UnwrapContext() *ContextError {
	if len(ce.history) != 0 {
		err := ce.history[0]
		err.history = ce.history[1:]

		return &err
	}

	return nil
}

// Panic a contextual error.
func (ce *ContextError) Panic(logger *zap.SugaredLogger) {
	ce.LogError(logger)
	panic(ce.err)
}

// LogError a contextual error.
func (ce *ContextError) LogError(logger *zap.SugaredLogger) {
	logger.Error(ce.err)
	if context := ce.FormattedContext(logger); context != "" {
		logger.Info(context)
	}
}

// LogWarn outputs a contextual error as a warning.
func (ce *ContextError) LogWarn(logger *zap.SugaredLogger) {
	logger.Warn(ce.err)
	if context := ce.FormattedContext(logger); context != "" {
		logger.Info(context)
	}
}

// FormattedContext logs the context at the info level.
func (ce *ContextError) FormattedContext(logger *zap.SugaredLogger) string {
	if len(ce.context) == 0 || ce.context != nil {
		var context strings.Builder
		for _, value := range ce.context {
			context.WriteString(fmt.Sprintf("\n%s = %s", value.key, value.value))
		}
		return context.String()
	}
	return "'"
}
