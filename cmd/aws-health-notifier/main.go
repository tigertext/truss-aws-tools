package main

import (
	"encoding/json"
	"log"

	"github.com/trussworks/truss-aws-tools/internal/aws/session"
	"github.com/trussworks/truss-aws-tools/internal/aws/ssm"
	"github.com/trussworks/truss-aws-tools/pkg/awshealth"

        "github.com/aws/aws-sdk-go/aws"
        "github.com/aws/aws-sdk-go/service/health"
	"github.com/aws/aws-lambda-go/events"
	"github.com/aws/aws-lambda-go/lambda"
	flag "github.com/jessevdk/go-flags"
	"github.com/lytics/slackhook"
	"go.uber.org/zap"
)

// Options are the command line options
type Options struct {
	Region             string `long:"region" description:"The AWS region to use." required:"false" env:"REGION"`
        // AWS Health API is available only in us-east-1 region
        AWSHealthRegion    string `long:"aws-health-region" description:"The AWS Health API region to use." required:"false" env:"AWS_HEALTH_REGION"`
        // To debug Lambda without sending an actual message to Slack
        DoNotSendMessage        bool `short:"s" long:"do-not-send-message" description:"Do not send message to Slack if true" required:"false" env:"DO_NOT_SEND_MESSAGE"`
	Profile            string `short:"p" long:"profile" description:"The AWS profile to use." required:"false" env:"AWS_PROFILE"`
	SlackChannel       string `long:"slack-channel" description:"The Slack channel." required:"true" env:"SLACK_CHANNEL"`
	SlackEmoji         string `long:"slack-emoji" description:"The Slack Emoji associated with the notifications." env:"SLACK_EMOJI" default:":boom:"`
	SSMSlackWebhookURL string `long:"ssm-slack-webhook-url" description:"The name of the Slack Webhook Url in Parameter store." required:"false" env:"SSM_SLACK_WEBHOOK_URL"`
}

var options Options
var logger *zap.Logger

func sendNotification(event events.CloudWatchEvent) {
	var healthEvent awshealth.Event
	err := json.Unmarshal([]byte(event.Detail), &healthEvent)
	if err != nil {
		logger.Error("Unable to unmarshal health event", zap.Error(err))
	}

	eventURL := healthEvent.HealthEventURL()
	awsSession := session.MustMakeSession(options.Region, options.Profile)
	slackWebhookURL, err := ssm.DecryptValue(awsSession, options.SSMSlackWebhookURL)
	if err != nil {
		logger.Fatal("failed to decrypt slackWebhookURL", zap.Error(err))
	}
	slack := slackhook.New(slackWebhookURL)

	description := "no description found in health check"
	if len(healthEvent.Description) > 0 {
		description = healthEvent.Description[0].Latest
	}

        awsAccountId := "N/A"
        entityValue := "N/A"

        awsSession2 := session.MustMakeSession(options.AWSHealthRegion, options.Profile)
        healthClient := health.New(awsSession2)
        eventArn := []*string{aws.String(healthEvent.EventARN)}
        entityFilter := &health.EntityFilter{
            EventArns: eventArn,
        }
        healthEventDetails, err := healthClient.DescribeAffectedEntities(&health.DescribeAffectedEntitiesInput{
                 Filter: entityFilter,
        })
        if err != nil {
	    log.Fatal(err)
        }
               
        if *healthEventDetails.Entities[0].AwsAccountId != "" {
            awsAccountId = *healthEventDetails.Entities[0].AwsAccountId
        }

        if *healthEventDetails.Entities[0].EntityValue != "" {
            entityValue = *healthEventDetails.Entities[0].EntityValue
        }

        logger.Info("AWS Account ID", zap.String("aws-account-id", awsAccountId))
        logger.Info("Entity Value", zap.String("entity-value", entityValue))

	attachment := slackhook.Attachment{
		Title:     "AWS Health Notification",
		TitleLink: awshealth.PersonalHealthDashboardURL,
		Color:     "danger",
		Fields: []slackhook.Field{
			{
				Title: "Service",
				Value: healthEvent.Service,
			},
			{
				Title: "Description",
				Value: description,
			},
			{
				Title: "EventTypeCode",
				Value: healthEvent.EventTypeCode,
			},
			{
				Title: "Link",
				Value: eventURL,
				Short: false,
			},
                        {
                                Title: "AWS Account ID",
                                Value: awsAccountId,
                                Short: false,
                        },
                        {
                                Title: "Entity Value",
                                Value: entityValue,
                                Short: false,
                        },
		},
	}

	message := &slackhook.Message {
		Channel:   options.SlackChannel,
		IconEmoji: options.SlackEmoji,
	}
	message.AddAttachment(&attachment)

        if options.DoNotSendMessage == true {
          logger.Info("Send message is turned off")
        } else {
	  err = slack.Send(message)
	  if err != nil {
		logger.Error("failed to send slack message", zap.Error(err),
			zap.String("slack-channel", options.SlackChannel))
	  }
	  logger.Info("successfully sent slack message", zap.String("slack-channel", options.SlackChannel))
       }
}

func lambdaHandler() {
	lambda.Start(sendNotification)
}

func main() {
	var err error
	logger, err = zap.NewProduction()
	if err != nil {
		log.Fatalf("can't initialize zap logger: %v", err)
	}
	parser := flag.NewParser(&options, flag.Default)

	_, err = parser.Parse()
	if err != nil {
		logger.Fatal("failed to parse flags", zap.Error(err))
	}

	logger.Info("Running Lambda handler.")
	lambdaHandler()
}
