package sqs

import "os"

const (
	// Visibility timeout da fila de transações. Deve ser maior que o tempo
	// máximo de tratamento de uma mensagem.
	VisibilityTimeoutSeconds = 30

	// Após MaxReceiveCount recebimentos sem remoção, o SQS move a mensagem
	// para a DLQ (redrive).
	MaxReceiveCount = 5
)

type Config struct {
	Endpoint  string
	Region    string
	AccessKey string
	SecretKey string

	// Nomes das filas (FIFO): SQS_QUEUE_NAME, SQS_DLQ_NAME e
	// SQS_EVENT_QUEUE_NAME.
	QueueName      string
	DLQName        string
	EventQueueName string
}

// LoadConfig lê a configuração do SQS do ambiente (LocalStack por padrão).
func LoadConfig() Config {
	return Config{
		Endpoint:  getenv("SQS_ENDPOINT", "http://localhost:4566"),
		Region:    getenv("AWS_REGION", "us-east-1"),
		AccessKey: getenv("AWS_ACCESS_KEY_ID", "test"),
		SecretKey: getenv("AWS_SECRET_ACCESS_KEY", "test"),

		QueueName:      getenv("SQS_QUEUE_NAME", "wager-transactions.fifo"),
		DLQName:        getenv("SQS_DLQ_NAME", "wager-transactions-dlq.fifo"),
		EventQueueName: getenv("SQS_EVENT_QUEUE_NAME", "wager-events.fifo"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
