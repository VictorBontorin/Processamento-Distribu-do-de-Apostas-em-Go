package sqs

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awssqs "github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

// waitReady aguarda o endpoint do SQS responder (LocalStack pode demorar).
func waitReady(ctx context.Context, client *awssqs.Client) error {
	var err error

	for attempt := 0; attempt < 30; attempt++ {
		if _, err = client.ListQueues(ctx, &awssqs.ListQueuesInput{}); err == nil {
			return nil
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}

	return fmt.Errorf("SQS endpoint not ready: %w", err)
}

// ChangeVisibility ajusta quando a mensagem volta a ficar visível.
// Com 0 segundos, libera a mensagem para reentrega imediata.
func (c *Client) ChangeVisibility(
	ctx context.Context,
	receiptHandle string,
	seconds int32,
) error {
	_, err := c.sqs.ChangeMessageVisibility(
		ctx,
		&awssqs.ChangeMessageVisibilityInput{
			QueueUrl:          aws.String(c.queueURL),
			ReceiptHandle:     aws.String(receiptHandle),
			VisibilityTimeout: seconds,
		},
	)
	if err != nil {
		return fmt.Errorf("change SQS message visibility: %w", err)
	}

	return nil
}

// SendToDLQ move uma mensagem permanentemente inválida para a DLQ.
func (c *Client) SendToDLQ(
	ctx context.Context,
	message Message,
	reason string,
) error {
	group := message.GroupID
	if group == "" {
		group = "invalid"
	}

	if len(reason) > 256 {
		reason = reason[:256]
	}

	_, err := c.sqs.SendMessage(ctx, &awssqs.SendMessageInput{
		QueueUrl:               aws.String(c.dlqURL),
		MessageBody:            aws.String(message.Body),
		MessageGroupId:         aws.String(group),
		MessageDeduplicationId: aws.String(message.ID),
		MessageAttributes: map[string]types.MessageAttributeValue{
			"failureReason": {
				DataType:    aws.String("String"),
				StringValue: aws.String(reason),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("send SQS message to DLQ: %w", err)
	}

	return nil
}

func parseCount(value string) int {
	count, err := strconv.Atoi(value)
	if err != nil || count < 1 {
		return 1
	}

	return count
}
