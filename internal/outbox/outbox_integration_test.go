package outbox_test

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"testing"
	"time"

	_ "github.com/lib/pq"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/discordia-grupo01/identify-service/internal/outbox"
)

func env(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return fallback
}

func newDB(t *testing.T) *sql.DB {
	t.Helper()
	host := os.Getenv("DATABASE_HOST")
	if host == "" {
		t.Skip("DATABASE_HOST no está seteado: este test requiere un Postgres real con las migraciones aplicadas")
	}
	dsn := fmt.Sprintf("host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		host, env("DATABASE_PORT", "5432"), env("DATABASE_USER", "discordia"), env("DATABASE_PASSWORD", "discordia"),
		env("DATABASE_NAME", "discordia_servicio"), env("DATABASE_SSLMODE", "disable"))
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`DELETE FROM outbox_events WHERE routing_key LIKE 'identity.test_%'`); err != nil {
		t.Fatalf("limpiar outbox: %v", err)
	}
	t.Cleanup(func() { _, _ = db.Exec(`DELETE FROM outbox_events WHERE routing_key LIKE 'identity.test_%'`) })
	return db
}

func tracing(t *testing.T) *tracetest.SpanRecorder {
	t.Helper()
	spans := tracetest.NewSpanRecorder()
	previousProvider := otel.GetTracerProvider()
	previousPropagator := otel.GetTextMapPropagator()
	otel.SetTracerProvider(sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans)))
	otel.SetTextMapPropagator(propagation.TraceContext{})
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return spans
}

func insert(t *testing.T, db *sql.DB, ctx context.Context, routingKey string) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := outbox.Insert(ctx, tx, routingKey, map[string]string{"k": "v"}); err != nil {
		_ = tx.Rollback()
		t.Fatalf("Insert: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func TestInsertStoresTheTraceparentOfTheRequest(t *testing.T) {
	tracing(t)
	db := newDB(t)
	ctx, span := otel.Tracer("test").Start(context.Background(), "request")
	defer span.End()

	insert(t, db, ctx, "identity.test_with_trace")

	var stored sql.NullString
	if err := db.QueryRow(`SELECT traceparent FROM outbox_events WHERE routing_key = 'identity.test_with_trace'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	want := "00-" + span.SpanContext().TraceID().String() + "-" + span.SpanContext().SpanID().String() + "-01"
	if !stored.Valid || stored.String != want {
		t.Errorf("traceparent guardado: %v, quería %q", stored, want)
	}
}

func TestInsertWithoutATraceStoresNull(t *testing.T) {
	tracing(t)
	db := newDB(t)

	insert(t, db, context.Background(), "identity.test_without_trace")

	var stored sql.NullString
	if err := db.QueryRow(`SELECT traceparent FROM outbox_events WHERE routing_key = 'identity.test_without_trace'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.Valid {
		t.Errorf("sin traza debería ser NULL, fue %q", stored.String)
	}
}

type capturingPublisher struct {
	published chan trace.SpanContext
}

func (p capturingPublisher) Publish(ctx context.Context, routingKey string, _ any) error {
	if routingKey == "identity.test_traced" || routingKey == "identity.test_untraced" {
		p.published <- trace.SpanContextFromContext(ctx)
	}
	return nil
}

func TestPollerPublishesInsideTheTraceOfTheRequest(t *testing.T) {
	spans := tracing(t)
	db := newDB(t)
	ctx, request := otel.Tracer("test").Start(context.Background(), "request")
	insert(t, db, ctx, "identity.test_traced")
	request.End()

	publisher := capturingPublisher{published: make(chan trace.SpanContext, 8)}
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go outbox.NewPoller(db, publisher).Start(pollCtx)

	select {
	case got := <-publisher.published:
		if got.TraceID() != request.SpanContext().TraceID() {
			t.Errorf("el publicador debería continuar la traza del pedido: %s", got.TraceID())
		}
	case <-time.After(2*outbox.PollInterval + 3*time.Second):
		t.Fatal("el poller no publicó el evento")
	}

	var producer sdktrace.ReadOnlySpan
	for _, span := range spans.Ended() {
		if span.Name() == "outbox publish identity.test_traced" {
			producer = span
		}
	}
	if producer == nil {
		t.Fatal("falta el span de publicación")
	}
	if producer.SpanKind() != trace.SpanKindProducer || producer.Parent().SpanID() != request.SpanContext().SpanID() {
		t.Errorf("el span de publicación debería colgar del pedido: %v", producer.Parent())
	}
}

func TestPollerPublishesEventsThatHaveNoTrace(t *testing.T) {
	tracing(t)
	db := newDB(t)
	insert(t, db, context.Background(), "identity.test_untraced")

	publisher := capturingPublisher{published: make(chan trace.SpanContext, 8)}
	pollCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go outbox.NewPoller(db, publisher).Start(pollCtx)

	select {
	case <-publisher.published:
	case <-time.After(2*outbox.PollInterval + 3*time.Second):
		t.Fatal("el poller no publicó un evento sin traza")
	}
}
