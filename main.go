package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/opsgenie/opsgenie-go-sdk-v2/client"
	"github.com/opsgenie/opsgenie-go-sdk-v2/schedule"
	"github.com/sirupsen/logrus"
	"github.com/slack-go/slack"
)

func main() {
	var (
		opsgenieApiKey = flag.String("opsgenie-api-key", os.Getenv("OPSGENIE_API_KEY"), "Opsgenie API Key. Will be read from environment 'OPSGENIE_API_KEY'")
		slackApiKey    = flag.String("slack-api-key", os.Getenv("SLACK_API_KEY"), "slack API Key. Will be read from environment 'SLACK_API_KEY'")

		opsgenieScheduleName = flag.String("opsgenie-schedule", "", "Opsgenie Team Schedule Name")
		slackGroupHandle     = flag.String("slack-group", "", "Slack Usergroup handle without @")
	)
	flag.Parse()
	var missingArgs []string
	flag.VisitAll(func(f *flag.Flag) {
		if f.Value.String() == "" {
			missingArgs = append(missingArgs, f.Name)
		}
	})
	if len(missingArgs) > 0 {
		fmt.Fprintf(os.Stderr, "Missing required arguments: %v\n\n", missingArgs)

		flag.PrintDefaults()
		os.Exit(1)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil)).With(
		slog.String("slackHandle", *slackGroupHandle),
		slog.String("opsgenieSchedule", *opsgenieScheduleName),
	)
	slog.SetDefault(logger)

	config := &client.Config{ApiKey: *opsgenieApiKey, LogLevel: logrus.ErrorLevel}
	s, err := schedule.NewClient(config)
	if err != nil {
		logger.Error("Error creating schedule client", slog.Any("error", err))
		os.Exit(1)
	}

	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGTERM)

	ctx, shutdown := context.WithCancel(context.Background())
	defer shutdown()

	go func() {
		<-sigs
		logger.Info("cancelling...")
		shutdown()
	}()

	flat := false
	req := &schedule.GetOnCallsRequest{
		ScheduleIdentifierType: schedule.Name,
		Flat:                   &flat,
		ScheduleIdentifier:     *opsgenieScheduleName,
	}
	res, err := s.GetOnCalls(ctx, req)
	if err != nil {
		logger.Error("Error getting on call participants", slog.Any("error", err))
		os.Exit(1)
	}

	if len(res.OnCallParticipants) == 0 {
		logger.Error("No on call participants found")
		os.Exit(1)
	}

	lookupEmail := res.OnCallParticipants[0].Name
	logger = logger.With(slog.String("oncallParticipant", lookupEmail))

	slackApi := slack.New(*slackApiKey)
	user, err := slackApi.GetUserByEmailContext(ctx, lookupEmail)

	userGroups, err := slackApi.GetUserGroupsContext(ctx)

	var userGroup *slack.UserGroup

	for _, group := range userGroups {
		if group.Handle == *slackGroupHandle {
			userGroup = &group
		}
	}

	if userGroup == nil {
		logger.Error("Slack usergroup not found")
		os.Exit(1)
	}

	_, err = slackApi.UpdateUserGroupMembersContext(ctx, userGroup.ID, user.ID)
	if err != nil {
		logger.Error("Error updating usergroup members", slog.Any("error", err))
		os.Exit(1)
	}

	logger.Info("User group updated")
}
