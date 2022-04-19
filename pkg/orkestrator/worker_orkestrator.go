package orkestrator

import (
	"context"
	"sync"
	"time"

	"github.com/conductor-sdk/conductor-go/pkg/conductor_client/conductor_http_client"
	"github.com/conductor-sdk/conductor-go/pkg/http_model"
	"github.com/conductor-sdk/conductor-go/pkg/metrics"
	"github.com/conductor-sdk/conductor-go/pkg/model"
	"github.com/conductor-sdk/conductor-go/pkg/model/enum/task_result_status"
	"github.com/conductor-sdk/conductor-go/pkg/settings"
	log "github.com/sirupsen/logrus"
)

type WorkerOrkestrator struct {
	conductorTaskResourceClient *conductor_http_client.TaskResourceApiService
	metricsCollector            *metrics.MetricsCollector
	waitGroup                   sync.WaitGroup
}

func NewWorkerOrkestrator(
	authenticationSettings *settings.AuthenticationSettings,
	httpSettings *settings.HttpSettings,
) *WorkerOrkestrator {
	metricsCollector := metrics.NewMetricsCollector()
	return &WorkerOrkestrator{
		conductorTaskResourceClient: conductor_http_client.NewTaskResourceApiService(
			authenticationSettings,
			httpSettings,
			metricsCollector,
		),
		metricsCollector: metricsCollector,
	}
}

func (c *WorkerOrkestrator) StartWorker(taskType string, executeFunction model.TaskExecuteFunction, parallelGoRoutinesAmount int, pollingInterval int) {
	for goRoutines := 1; goRoutines <= parallelGoRoutinesAmount; goRoutines++ {
		c.waitGroup.Add(1)
		go c.run(taskType, executeFunction, pollingInterval)
	}
	log.Debug(
		"Started worker for task: ", taskType,
		", go routines amount: ", parallelGoRoutinesAmount,
		", polling interval: ", pollingInterval, "ms",
	)
}

func (c *WorkerOrkestrator) WaitWorkers() {
	c.waitGroup.Wait()
}

func (c *WorkerOrkestrator) run(taskType string, executeFunction model.TaskExecuteFunction, pollingInterval int) {
	for {
		c.runOnce(taskType, executeFunction, pollingInterval)
	}
	c.waitGroup.Done()
}

func (c *WorkerOrkestrator) runOnce(taskType string, executeFunction model.TaskExecuteFunction, pollingInterval int) {
	task := c.pollTask(taskType)
	if task == nil {
		sleep(pollingInterval)
		return
	}
	taskResult := c.executeTask(task, executeFunction)
	c.updateTask(taskType, taskResult)
}

func (c *WorkerOrkestrator) pollTask(taskType string) *http_model.Task {
	c.metricsCollector.IncrementTaskPoll(taskType)
	startTime := time.Now()
	task, response, err := c.conductorTaskResourceClient.Poll(
		context.Background(),
		taskType,
		nil,
	)
	spentTime := time.Since(startTime)
	c.metricsCollector.RecordTaskPollTime(
		taskType,
		spentTime.Seconds(),
	)
	if response.StatusCode == 204 {
		return nil
	}
	if err != nil {
		log.Error(
			"Error polling for task: ", taskType,
			", error: ", err.Error(),
		)
		c.metricsCollector.IncrementTaskPollError(
			taskType, err,
		)
		return nil
	}
	log.Debug("Polled task: ", task)
	return &task
}

func (c *WorkerOrkestrator) executeTask(t *http_model.Task, executeFunction model.TaskExecuteFunction) *http_model.TaskResult {
	startTime := time.Now()
	taskResult, err := executeFunction(t)
	spentTime := time.Since(startTime)
	c.metricsCollector.RecordTaskExecuteTime(
		t.TaskDefName, spentTime.Seconds(),
	)
	if taskResult == nil {
		log.Error("TaskResult cannot be nil: ", t.TaskId)
		return nil
	}
	if err != nil {
		log.Error("Error Executing task:", err.Error())
		taskResult.Status = task_result_status.FAILED
		taskResult.ReasonForIncompletion = err.Error()
		c.metricsCollector.IncrementTaskExecuteError(
			t.TaskDefName, err,
		)
	}
	log.Debug("Executed task: ", *t)
	return taskResult
}

func (c *WorkerOrkestrator) updateTask(taskType string, taskResult *http_model.TaskResult) {
	_, response, err := c.conductorTaskResourceClient.UpdateTask(
		taskType,
		context.Background(),
		taskResult,
	)
	if err != nil {
		log.Error(
			"Error on task update. taskResult: ", *taskResult,
			", error: ", err.Error(),
			", response: ", response,
		)
		c.metricsCollector.IncrementTaskUpdateError(taskType, err)
		return
	}
	log.Debug("Updated task: ", *taskResult)
}

func sleep(pollingInterval int) {
	time.Sleep(
		time.Duration(pollingInterval) * time.Millisecond,
	)
}