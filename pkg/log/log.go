package log

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type Fields map[string]any

type Logger struct {
	newLogger *zap.Logger
	fields    Fields
	callers   []string
}

// EncodeType 日志输出类型，支持控制台和json格式
type EncodeType int

const (
	// EncodeTypeConsole 控制台输出
	EncodeTypeConsole EncodeType = iota
	// EncodeTypeJson json输出
	EncodeTypeJson
)

type Option struct {
	Hook        io.Writer
	Mode        string
	ServiceName string
	EncodeType  EncodeType
}

var (
	GlobalLogger *Logger
	once         sync.Once
)

func NewLogger(opt *Option) *Logger {
	once.Do(func() {
		GlobalLogger = newLogger(opt)
	})
	return GlobalLogger
}

func newLogger(opt *Option) *Logger {
	encoderConfig := zapcore.EncoderConfig{
		TimeKey:       "time",
		LevelKey:      "level",
		NameKey:       "service",
		CallerKey:     "tag",
		MessageKey:    "msg",
		StacktraceKey: "stacktrace",
		LineEnding:    zapcore.DefaultLineEnding,
		EncodeLevel:   zapcore.LowercaseLevelEncoder, // 小写编码器
		EncodeTime: func(time time.Time, encoder zapcore.PrimitiveArrayEncoder) {
			encoder.AppendString(time.Format("2006-01-02 15:04:05"))
		}, // ISO8601 UTC 时间格式
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.FullCallerEncoder, // 全路径编码器
		EncodeName:     zapcore.FullNameEncoder,
	}

	// 输出文件和行号，前提是配置对象encoderConfig中必须设有CallerKey字段
	caller := zap.AddCaller()
	// 由于再次封装日志，因此需要打印上一级的调用，1表示向上跳一级
	callerSkip := zap.AddCallerSkip(1)

	// 编码器设置
	var encoder zapcore.Encoder
	if opt.EncodeType == EncodeTypeConsole {
		encoder = zapcore.NewConsoleEncoder(encoderConfig)
	} else {
		encoder = zapcore.NewJSONEncoder(encoderConfig)
	}
	// 打印设置，支持打印到多处
	// 打印到控制台：zapcore.AddSync(os.Stdout)
	// 打印到日志文件：zapcore.AddSync(opt.Hook)
	// writeSyncer := zapcore.NewMultiWriteSyncer(zapcore.AddSync(opt.Hook))
	writeSyncer := zapcore.NewMultiWriteSyncer(zapcore.AddSync(os.Stdout))

	if opt.Mode == "debug" || opt.Mode == "test" {
		core := zapcore.NewCore(encoder, writeSyncer, zap.DebugLevel)
		// 开启开发模式
		return &Logger{
			newLogger: zap.New(core, caller, callerSkip, zap.Development()).Named(opt.ServiceName),
		}
	}

	// 设置日志级别
	atomicLevel := zap.NewAtomicLevel()
	atomicLevel.SetLevel(zap.InfoLevel)
	core := zapcore.NewCore(encoder, writeSyncer, atomicLevel)

	return &Logger{
		newLogger: zap.New(core, caller, callerSkip).Named(opt.ServiceName),
	}
}

func (l *Logger) clone() *Logger {
	nl := *l
	return &nl
}

func (l *Logger) WithFields(f Fields) *Logger {
	ll := l.clone()
	if ll.fields == nil {
		ll.fields = make(Fields)
	}
	for k, v := range f {
		ll.fields[k] = v
	}
	return ll
}

func (l *Logger) WithCallersFrames() *Logger {
	maxCallerDepth := 25
	minCallerDepth := 1
	var callers []string
	pcs := make([]uintptr, maxCallerDepth)
	depth := runtime.Callers(minCallerDepth, pcs)
	frames := runtime.CallersFrames(pcs[:depth])
	for frame, more := frames.Next(); more; frame, more = frames.Next() {
		callers = append(callers, fmt.Sprintf("%s: %d %s", frame.File, frame.Line, frame.Function))
		if !more {
			break
		}
	}
	ll := l.clone()
	ll.callers = callers
	return ll
}

func (l *Logger) Debug(msg string) {
	if l.fields != nil {
		l.newLogger.Debug(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Debug(msg)
}

func (l *Logger) Debugf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if l.fields != nil {
		l.newLogger.Debug(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Debug(msg)
}

func (l *Logger) Info(msg string) {
	if l.fields != nil {
		l.newLogger.Info(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Info(msg)
}

func (l *Logger) Infof(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if l.fields != nil {
		l.newLogger.Info(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Info(msg)
}

func (l *Logger) Warn(msg string) {
	if l.fields != nil {
		l.newLogger.Warn(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Warn(msg)
}

func (l *Logger) Warnf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if l.fields != nil {
		l.newLogger.Warn(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Warn(msg)
}

func (l *Logger) Error(msg string) {
	if l.fields != nil {
		l.newLogger.Error(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Error(msg)
}

func (l *Logger) Errorf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if l.fields != nil {
		l.newLogger.Error(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Error(msg)
}

func (l *Logger) Fatal(msg string) {
	if l.fields != nil {
		l.newLogger.Fatal(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Fatal(msg)
}

func (l *Logger) Fatalf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if l.fields != nil {
		l.newLogger.Fatal(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Fatal(msg)
}

func (l *Logger) Panic(msg string) {
	if l.fields != nil {
		l.newLogger.Panic(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Panic(msg)
}

func (l *Logger) Panicf(format string, v ...any) {
	msg := fmt.Sprintf(format, v...)
	if l.fields != nil {
		l.newLogger.Panic(msg, zap.Any("field", l.fields))
		return
	}
	ll := l.clone()
	ll.newLogger.Panic(msg)
}
