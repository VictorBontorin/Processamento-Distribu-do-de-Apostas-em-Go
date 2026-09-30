package sqs

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sqs/types"
)

const (
	QueueName      = "wager-transactions.fifo"
	DLQName        = "wager-transactions-dlq.fifo"
	EventQueueName = "wager-events.fifo"
)

type Client struct {
	sqs           *sqs.Client
	queueURL      string
	dlqURL        string
	eventQueueURL string
}

func NewClient(ctx context.Context, cfg Config) (*Client, error) {
	cfg, err := config.LoadDefaultConfig(
		ctx,
		config.WithRegion(cfg.Region),
		config.WithCredentialsProvider(
			credentials.NewStaticCredentialsProvider(
				cfg.AccessKey,
				cfg.SecretKey,
				"",
			),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("load AWS config: %w", err)
	}

	client := sqs.NewFromConfig(cfg, func(options *sqs.Options) {
		options.BaseEndpoint = aws.String(cfg.Endpoint)
	})

	if err := waitReady(ctx, client); err != nil {
		return nil, err
	}

	dlqURL, err := ensureQueue(ctx, client, DLQName, false, "")
	if err != nil {
		return nil, fmt.Errorf("create DLQ: %w", err)
	}

	dlqARN, err := getQueueARN(ctx, client, dlqURL)
	if err != nil {
		return nil, fmt.Errorf("get DLQ ARN: %w", err)
	}

	redrivePolicy, err := json.Marshal(map[string]any{
		"deadLetterTargetArn": dlqARN,
		"maxReceiveCount":     5,
	})
	if err != nil {
		return nil, fmt.Errorf("build redrive policy: %w", err)
	}

	queueURL, err := ensureQueue(
		ctx,
		client,
		QueueName,
		true,
		string(redrivePolicy),
	)
	if err != nil {
		return nil, fmt.Errorf("create transaction queue: %w", err)
	}

	eventQueueURL, err := ensureQueue(
		ctx,
		client,
		EventQueueName,
		true,
		"",
	)
	if err != nil {
		return nil, fmt.Errorf("create event queue: %w", err)
	}

	return &Client{
		sqs:           client,
		queueURL:      queueURL,
		dlqURL:        dlqURL,
		eventQueueURL: eventQueueURL,
	}, nil
}

func ensureQueue(
	ctx context.Context,
	client *sqs.Client,
	name string,
	fifo bool,
	redrivePolicy string,
) (string, error) {
	// Se a fila já existe, reutiliza.
	queueOutput, err := client.GetQueueUrl(
		ctx,
		&sqs.GetQueueUrlInput{
			QueueName: aws.String(name),
		},
	)

	if err == nil {
		return aws.ToString(queueOutput.QueueUrl), nil
	}

	attributes := map[string]string{}

	if fifo {
		attributes["FifoQueue"] = "true"
		attributes["ContentBasedDeduplication"] = "false"
		attributes["VisibilityTimeout"] = "30"

		if redrivePolicy != "" {
			attributes["RedrivePolicy"] = redrivePolicy
		}
	}

	createOutput, err := client.CreateQueue(
		ctx,
		&sqs.CreateQueueInput{
			QueueName:  aws.String(name),
			Attributes: attributes,
		},
	)
	if err != nil {
		return "", err
	}

	return aws.ToString(createOutput.QueueUrl), nil
}

func getQueueARN(
	ctx context.Context,
	client *sqs.Client,
	queueURL string,
) (string, error) {
	output, err := client.GetQueueAttributes(
		ctx,
		&sqs.GetQueueAttributesInput{
			QueueUrl: aws.String(queueURL),
			AttributeNames: []types.QueueAttributeName{
				types.QueueAttributeNameQueueArn,
			},
		},
	)
	if err != nil {
		return "", err
	}

	arn := output.Attributes[string(types.QueueAttributeNameQueueArn)]
	if arn == "" {
		return "", fmt.Errorf("queue ARN is empty")
	}

	return arn, nil
}

func (c *Client) Send(
	ctx context.Context,
	body string,
	messageGroupID string,
	deduplicationID string,
) error {
	_, err := c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(c.queueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(messageGroupID),
		MessageDeduplicationId: aws.String(deduplicationID),
	})
	if err != nil {
		return fmt.Errorf("send SQS message: %w", err)
	}

	return nil
}

func (c *Client) SendEvent(
	ctx context.Context,
	body string,
	messageGroupID string,
	deduplicationID string,
) error {
	_, err := c.sqs.SendMessage(ctx, &sqs.SendMessageInput{
		QueueUrl:               aws.String(c.eventQueueURL),
		MessageBody:            aws.String(body),
		MessageGroupId:         aws.String(messageGroupID),
		MessageDeduplicationId: aws.String(deduplicationID),
	})
	if err != nil {
		return fmt.Errorf("send SQS event: %w", err)
	}

	return nil
}

func (c *Client) Receive(
	ctx context.Context,
) ([]Message, error) {
	output, err := c.sqs.ReceiveMessage(
		ctx,
		&sqs.ReceiveMessageInput{
			QueueUrl:            aws.String(c.queueURL),
			MaxNumberOfMessages: 10,
			WaitTimeSeconds:     20,
			VisibilityTimeout:   VisibilityTimeoutSeconds,
			MessageSystemAttributeNames: []types.MessageSystemAttributeName{
				types.MessageSystemAttributeNameAll,
			},
		},
	)
	if err != nil {
		return nil, fmt.Errorf("receive SQS messages: %w", err)
	}

	messages := make([]Message, 0, len(output.Messages))

	for _, message := range output.Messages {
		messages = append(messages, Message{
			ID:            aws.ToString(message.MessageId),
			ReceiptHandle: aws.ToString(message.ReceiptHandle),
			Body:          aws.ToString(message.Body),
			GroupID:       message.Attributes["MessageGroupId"],
			ReceiveCount:  parseCount(message.Attributes["ApproximateReceiveCount"]),
		})
	}

	return messages, nil
}

func (c *Client) Delete(
	ctx context.Context,
	receiptHandle string,
) error {
	_, err := c.sqs.DeleteMessage(
		ctx,
		&sqs.DeleteMessageInput{
			QueueUrl:      aws.String(c.queueURL),
			ReceiptHandle: aws.String(receiptHandle),
		},
	)
	if err != nil {
		return fmt.Errorf("delete SQS message: %w", err)
	}

	return nil
}

type Message struct {
	ID            string
	ReceiptHandle string
	Body          string
	GroupID       string
	ReceiveCount  int
}

func (c *Client) Close(ctx context.Context) error {
	return nil
}
