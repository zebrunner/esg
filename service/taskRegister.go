package service

func RegisterTask(ctx context.Context, env *environment.ExecutionEnvironment) (taskArn string, returnErr error) {
	svc := ecs.New(AwsSess)

	family, err := env.GetFamilyRevision()
	if err != nil {
		return "", fmt.Errorf("%s: '%s'", ImageNotFound, env.TaskDefinitionFamily)
	}
	l := log.WithField("family", family)

	runTaskInput := &ecs.RunTaskInput{
		Cluster:        &config.Conf.AwsCluster,
		TaskDefinition: &family,
		Overrides:      &ecs.TaskOverride{ContainerOverrides: env.ContainerOverrides()},
		PlacementStrategy: []*ecs.PlacementStrategy{
			{
				Field: aws.String("memory"),
				Type:  aws.String("binpack"),
			},
		},
	}
	log.WithField("runTaskInput", runTaskInput).Trace("Res runTaskInput")

	// TODO: explicitly minimize errors range to wait only by well-known reasons aka RESOURCE:CPU etc
	// TODO: convert existing hard-coded 25 retries into the queue or provisioning timeout: https://github.com/zebrunner/esg/issues/72
	// [VD] "i" retry should be ~15 if instances can be started in 1 min and 25 if ~2 min
	var outputErr error
	for i := 0; i < 25; i++ {

		l = l.WithField("retry", i)

		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}
		// Random sleep to fix problems with parallel 100+ threads startup. Not applicable for generic tasks!
		//TODO: uncomment before release!
		/*		if env.TaskDefinitionFamily != "generic" {
					sleep := time.Duration(rand.Intn(30)) * time.Second
					time.Sleep(sleep)
				}
		*/

		var resultRunTask *ecs.RunTaskOutput
		resultRunTask, err := svc.RunTask(runTaskInput)
		// Not good solution but aws doesn't give a choice
		if err != nil && err.Error() == "ClientException: TaskDefinition not found." {
			return "", fmt.Errorf("%s: '%s'", ImageNotFound, env.TaskDefinitionFamily)
		}

		if err != nil &&
			(strings.Contains(err.Error(), "ThrottlingException: Rate exceeded") || err.Error() == "ClientException: Tasks provisioning capacity limit exceeded.") {
			time.Sleep(time.Duration(15 + rand.Intn(15)))
		}

		if err != nil {
			l.WithError(err).Debug("Run task failed.")
			outputErr = err
			continue
		}

		if len(resultRunTask.Failures) != 0 {
			l.WithField("error", *resultRunTask.Failures[0].Reason).Debug("Run task failed. Response contains failures")
			outputErr = errors.New("response contains failures")
			continue
		}

		if len(resultRunTask.Tasks) == 0 {
			l.Debug("Run task failed. Response doesn't contains tasks")
			outputErr = errors.New("response doesn't contains tasks")
			continue
		}

		// All is ok. We got task then we can return it.
		return *resultRunTask.Tasks[0].TaskArn, nil
	}

	return "", outputErr
}