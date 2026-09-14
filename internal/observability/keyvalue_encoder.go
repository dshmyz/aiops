package observability

import (
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap/buffer"
	"go.uber.org/zap/zapcore"
)

// keyValueEncoder renders structured fields as human-readable key=value pairs
// on a single line, instead of the JSON object that zap's built-in console
// encoder always appends. It is used only for COPILOT_LOG_FORMAT=console so dev
// logs (notably the access log) are readable while still keeping every field.
// Production (json) logging is unaffected.
type keyValueEncoder struct {
	cfg    zapcore.EncoderConfig
	sep    string
	prefix string // namespace prefix for nested keys (rarely used)
	buf    *buffer.Buffer
}

var kvBufferPool = buffer.NewPool()

// newKeyValueEncoder builds the encoder from an EncoderConfig. The metadata
// encoders (EncodeTime/EncodeLevel/EncodeCaller) are reused as-is so colors and
// layout match the rest of console output.
func newKeyValueEncoder(cfg zapcore.EncoderConfig) zapcore.Encoder {
	if cfg.ConsoleSeparator == "" {
		cfg.ConsoleSeparator = "\t"
	}
	return &keyValueEncoder{cfg: cfg, sep: cfg.ConsoleSeparator, buf: kvBufferPool.Get()}
}

func (e *keyValueEncoder) Clone() zapcore.Encoder {
	nc := *e
	nc.buf = kvBufferPool.Get()
	nc.buf.Write(e.buf.Bytes())
	return &nc
}

// appendKey writes "prefixkey=" (or just "key=") to the current buffer.
func (e *keyValueEncoder) appendKey(key string) {
	e.buf.AppendString(e.prefix)
	e.buf.AppendString(key)
	e.buf.AppendString("=")
}

// appendScalar writes "key=value" (space-separated from the previous field).
func (e *keyValueEncoder) appendScalar(key, value string) {
	if e.buf.Len() > 0 {
		e.buf.AppendString(e.sep)
	}
	e.appendKey(key)
	e.buf.AppendString(value)
}

// appendQuoted writes key="value" so strings containing spaces/escapes stay
// unambiguous on one line.
func (e *keyValueEncoder) appendQuoted(key, value string) {
	e.appendScalar(key, strconv.Quote(value))
}

// appendReflectedJSON serializes an arbitrary value as its JSON form.
func (e *keyValueEncoder) appendReflectedJSON(key string, value any) {
	raw, err := json.Marshal(value)
	if err != nil {
		e.appendQuoted(key, fmt.Sprintf("%v", value))
		return
	}
	e.appendScalar(key, string(raw))
}

// appendNestedJSON renders an ObjectMarshaler/ArrayMarshaler field as a compact
// JSON value via a scratch JSON encoder, so complex fields are never lost
// (access-log and most console fields are scalars and stay key=value).
func (e *keyValueEncoder) appendNestedJSON(key string, f zapcore.Field) error {
	scratch := zapcore.NewJSONEncoder(zapcore.EncoderConfig{LineEnding: "\n"})
	b, err := scratch.EncodeEntry(zapcore.Entry{}, []zapcore.Field{f})
	if b != nil {
		defer b.Free()
	}
	if err != nil {
		e.appendKey(key)
		return err
	}
	// Output is {"<key>": <value>}; pull out just the value to render key=<value>.
	var m map[string]json.RawMessage
	if json.Unmarshal(b.Bytes(), &m) == nil {
		if raw, ok := m[key]; ok {
			e.appendScalar(key, string(raw))
			return nil
		}
	}
	e.appendScalar(key, strings.TrimSpace(b.String()))
	return nil
}

// --- ObjectEncoder primitives ---

func (e *keyValueEncoder) AddBool(key string, v bool) { e.appendScalar(key, strconv.FormatBool(v)) }

func (e *keyValueEncoder) AddInt(key string, v int)     { e.AddInt64(key, int64(v)) }
func (e *keyValueEncoder) AddInt8(key string, v int8)   { e.AddInt64(key, int64(v)) }
func (e *keyValueEncoder) AddInt16(key string, v int16) { e.AddInt64(key, int64(v)) }
func (e *keyValueEncoder) AddInt32(key string, v int32) { e.AddInt64(key, int64(v)) }
func (e *keyValueEncoder) AddInt64(key string, v int64) {
	e.appendScalar(key, strconv.FormatInt(v, 10))
}

func (e *keyValueEncoder) AddUint(key string, v uint)     { e.AddUint64(key, uint64(v)) }
func (e *keyValueEncoder) AddUint8(key string, v uint8)   { e.AddUint64(key, uint64(v)) }
func (e *keyValueEncoder) AddUint16(key string, v uint16) { e.AddUint64(key, uint64(v)) }
func (e *keyValueEncoder) AddUint32(key string, v uint32) { e.AddUint64(key, uint64(v)) }
func (e *keyValueEncoder) AddUint64(key string, v uint64) {
	e.appendScalar(key, strconv.FormatUint(v, 10))
}

func (e *keyValueEncoder) AddUintptr(key string, v uintptr) { e.AddUint64(key, uint64(v)) }

func (e *keyValueEncoder) AddFloat32(key string, v float32) {
	e.appendScalar(key, strconv.FormatFloat(float64(v), 'g', -1, 32))
}

func (e *keyValueEncoder) AddFloat64(key string, v float64) {
	e.appendScalar(key, strconv.FormatFloat(v, 'g', -1, 64))
}

func (e *keyValueEncoder) AddComplex64(key string, v complex64) {
	e.appendScalar(key, fmt.Sprintf("%v", v))
}

func (e *keyValueEncoder) AddComplex128(key string, v complex128) {
	e.appendScalar(key, fmt.Sprintf("%v", v))
}

func (e *keyValueEncoder) AddDuration(key string, v time.Duration) {
	e.appendScalar(key, v.String())
}

func (e *keyValueEncoder) AddTime(key string, v time.Time) {
	e.appendScalar(key, v.Format(time.RFC3339Nano))
}

func (e *keyValueEncoder) AddString(key, value string) { e.appendQuoted(key, value) }
func (e *keyValueEncoder) AddByteString(key string, v []byte) {
	e.appendQuoted(key, string(v))
}
func (e *keyValueEncoder) AddBinary(key string, v []byte) { e.appendQuoted(key, string(v)) }

func (e *keyValueEncoder) AddStringer(key string, v fmt.Stringer) {
	e.appendQuoted(key, v.String())
}

func (e *keyValueEncoder) AddReflected(key string, v any) error {
	e.appendReflectedJSON(key, v)
	return nil
}

func (e *keyValueEncoder) AddObject(key string, v zapcore.ObjectMarshaler) error {
	return e.appendNestedJSON(key, zapcore.Field{Key: key, Type: zapcore.ObjectMarshalerType, Interface: v})
}

func (e *keyValueEncoder) AddArray(key string, v zapcore.ArrayMarshaler) error {
	return e.appendNestedJSON(key, zapcore.Field{Key: key, Type: zapcore.ArrayMarshalerType, Interface: v})
}

func (e *keyValueEncoder) OpenNamespace(key string) {
	// Flat console rendering: namespaces just prefix nested keys, never lost.
	e.prefix += key + "."
}

// --- Encoder ---

// EncodeEntry assembles the single-line record: metadata (time/level/caller)
// and message, then the structured fields as key=value.
func (e *keyValueEncoder) EncodeEntry(ent zapcore.Entry, fields []zapcore.Field) (*buffer.Buffer, error) {
	line := kvBufferPool.Get()
	write := func(s string) {
		if line.Len() > 0 {
			line.AppendString(e.sep)
		}
		line.AppendString(s)
	}

	if e.cfg.TimeKey != "" && e.cfg.EncodeTime != nil && !ent.Time.IsZero() {
		sink := new(metaSink)
		e.cfg.EncodeTime(ent.Time, sink)
		if sink.s != "" {
			write(sink.s)
		}
	}
	if e.cfg.LevelKey != "" && e.cfg.EncodeLevel != nil {
		sink := new(metaSink)
		e.cfg.EncodeLevel(ent.Level, sink)
		if sink.s != "" {
			write(sink.s)
		}
	}
	if ent.LoggerName != "" && e.cfg.NameKey != "" {
		nameEnc := e.cfg.EncodeName
		if nameEnc == nil {
			nameEnc = zapcore.FullNameEncoder
		}
		sink := new(metaSink)
		nameEnc(ent.LoggerName, sink)
		if sink.s != "" {
			write(sink.s)
		}
	}
	if ent.Caller.Defined && e.cfg.CallerKey != "" && e.cfg.EncodeCaller != nil {
		sink := new(metaSink)
		e.cfg.EncodeCaller(ent.Caller, sink)
		if sink.s != "" {
			write(sink.s)
		}
	}
	if ent.Caller.Defined && e.cfg.FunctionKey != "" {
		write(ent.Caller.Function)
	}
	if e.cfg.MessageKey != "" && ent.Message != "" {
		write(ent.Message)
	}

	if len(fields) > 0 {
		ctx := e.Clone().(*keyValueEncoder)
		addFieldsTo(ctx, fields)
		if ctx.buf.Len() > 0 {
			write(ctx.buf.String())
		}
		ctx.buf.Free()
	}

	line.AppendString(e.cfg.LineEnding)
	return line, nil
}

// addFieldsTo dispatches structured fields onto the encoder, mirroring
// zapcore.addFields (unexported) so nested object/array/reflected fields are
// still captured.
func addFieldsTo(e *keyValueEncoder, fields []zapcore.Field) {
	for _, f := range fields {
		switch f.Type {
		case zapcore.BoolType:
			e.AddBool(f.Key, f.Integer == 1)
		case zapcore.Int64Type:
			e.AddInt64(f.Key, f.Integer)
		case zapcore.Float64Type:
			e.AddFloat64(f.Key, math.Float64frombits(uint64(f.Integer)))
		case zapcore.Float32Type:
			e.AddFloat32(f.Key, math.Float32frombits(uint32(f.Integer)))
		case zapcore.DurationType:
			e.AddDuration(f.Key, time.Duration(f.Integer))
		case zapcore.StringType:
			e.AddString(f.Key, f.String)
		case zapcore.ErrorType:
			e.AddString(f.Key, errFromField(f.Interface).Error())
		case zapcore.TimeType:
			e.AddTime(f.Key, time.Unix(0, f.Integer))
		case zapcore.ByteStringType:
			e.AddByteString(f.Key, []byte(f.String))
		case zapcore.BinaryType:
			e.AddBinary(f.Key, []byte(f.String))
		case zapcore.StringerType:
			e.AddStringer(f.Key, f.Interface.(fmt.Stringer))
		case zapcore.ReflectType:
			_ = e.AddReflected(f.Key, f.Interface)
		case zapcore.ObjectMarshalerType:
			_ = e.AddObject(f.Key, f.Interface.(zapcore.ObjectMarshaler))
		case zapcore.ArrayMarshalerType:
			_ = e.AddArray(f.Key, f.Interface.(zapcore.ArrayMarshaler))
		case zapcore.NamespaceType:
			e.OpenNamespace(f.Key)
		case zapcore.Complex64Type:
			e.AddComplex64(f.Key, f.Interface.(complex64))
		case zapcore.Complex128Type:
			e.AddComplex128(f.Key, f.Interface.(complex128))
		default:
			// SkipType / unknown: keep the raw interface, never drop.
			if f.Interface != nil {
				_ = e.AddReflected(f.Key, f.Interface)
			}
		}
	}
}

// errFromField extracts the error carried by an ErrorType field.
func errFromField(v any) error {
	if err, ok := v.(error); ok {
		return err
	}
	return fmt.Errorf("unknown error field")
}

// metaSink implements zapcore.PrimitiveArrayEncoder so the EncoderConfig
// EncodeTime/EncodeLevel/EncodeCaller/EncodeName are reusable; the standard
// encoders only ever call AppendString. All other methods are safe no-ops.
type metaSink struct{ s string }

func (m *metaSink) AppendString(s string)              { m.s += s }
func (m *metaSink) AppendBool(bool)                    {}
func (m *metaSink) AppendByteString([]byte)            {}
func (m *metaSink) AppendComplex128(complex128)        {}
func (m *metaSink) AppendComplex64(complex64)          {}
func (m *metaSink) AppendDuration(time.Duration)       {}
func (m *metaSink) AppendFloat32(float32)              {}
func (m *metaSink) AppendFloat64(float64)              {}
func (m *metaSink) AppendInt(int)                      {}
func (m *metaSink) AppendInt8(int8)                    {}
func (m *metaSink) AppendInt16(int16)                  {}
func (m *metaSink) AppendInt32(int32)                  {}
func (m *metaSink) AppendInt64(int64)                  {}
func (m *metaSink) AppendTime(time.Time)               {}
func (m *metaSink) AppendUint(uint)                    {}
func (m *metaSink) AppendUint8(uint8)                  {}
func (m *metaSink) AppendUint16(uint16)                {}
func (m *metaSink) AppendUint32(uint32)                {}
func (m *metaSink) AppendUint64(uint64)                {}
func (m *metaSink) AppendUintptr(uintptr)              {}
func (m *metaSink) AppendArray(zapcore.ArrayMarshaler) {}
func (m *metaSink) AppendObject(zapcore.ObjectMarshaler) {
}
func (m *metaSink) AppendReflected(any) {}