package sqs

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
)

// Ping verifica se o SQS responde e se a fila principal está acessível.
func (c *Client) Ping(ctx context.Context) error {
	_, err := c.sqs.GetQueueAttributes(ctx, &awssqs.GetQueueAttributesInput{
		QueueUrl: aws.String(c.queueURL),
	})
	if err != nil {
		return fmt.Errorf("ping SQS: %w", err)
	}

	return nil
}
