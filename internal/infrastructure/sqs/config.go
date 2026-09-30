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
}

// LoadConfig lê a configuração do SQS do ambiente (LocalStack por padrão).
func LoadConfig() Config {
	return Config{
		Endpoint:  getenv("SQS_ENDPOINT", "http://localhost:4566"),
		Region:    getenv("AWS_REGION", "us-east-1"),
		AccessKey: getenv("AWS_ACCESS_KEY_ID", "test"),
		SecretKey: getenv("AWS_SECRET_ACCESS_KEY", "test"),
	}
}

func getenv(key, fallback string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}

	return fallback
}
