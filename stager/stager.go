package stager

import (
	"context"
	"runtime"
	"sync"
	"time"

	"github.com/pkg/errors"
	"github.com/sirupsen/logrus"

	"github.com/projecteru2/phistage/common"
	"github.com/projecteru2/phistage/executors"
	"github.com/projecteru2/phistage/store"
)

type Stager struct {
	config *common.Config
	stages chan *common.Phistage
	stop   chan struct{}
	store  store.Store
	wg     sync.WaitGroup
}

func NewStager(store store.Store, config *common.Config) *Stager {
	return &Stager{
		config: config,
		stages: make(chan *common.Phistage),
		stop:   make(chan struct{}),
		store:  store,
		wg:     sync.WaitGroup{},
	}
}

func (s *Stager) Start() {
	for i := 0; i < s.config.StagerWorkers; i++ {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.processStages()
		}()
	}
}

func (s *Stager) Stop() {
	close(s.stop)
	s.wg.Wait()
}

func (s *Stager) Add(phistage *common.Phistage) {
	s.stages <- phistage
}

func (s *Stager) processStages() {
	for {
		select {
		case <-s.stop:
			return
		case phistage := <-s.stages:
			if err := s.runWithGraph(phistage); err != nil {
				logrus.WithField("phistage", phistage.Name).WithError(err).Errorf("[Stager process] error when running a phistage")
			}
			runtime.GC()
		}
	}
}

func (s *Stager) runWithGraph(phistage *common.Phistage) error {
	logger := logrus.WithFields(logrus.Fields{"phistage": phistage.Name})

	if err := s.store.CreatePhistage(context.TODO(), phistage); err != nil {
		logger.WithError(err).Error("[Stager runWithGraph] fail to create Phistage")
		return err
	}

	jobGraph, err := phistage.JobDependencies()
	if err != nil {
		logger.WithError(err).Errorf("[Stager runWithGraph] error getting job graph")
		return err
	}

	run := &common.Run{
		Phistage: phistage.Name,
		Start:    time.Now(),
	}
	if err := s.store.CreateRun(context.TODO(), run); err != nil {
		logger.WithError(err).Error("[Stager runWithGraph] fail to create Run")
		return err
	}
	defer func() {
		run.End = time.Now()
		if err := s.store.UpdateRun(context.TODO(), run); err != nil {
			logger.WithError(err).Errorf("[Stager runWithGraph] error update Run")
		}
	}()

	for _, jobs := range jobGraph {
		wg := sync.WaitGroup{}
		for _, job := range jobs {
			wg.Add(1)
			go func(job *common.Job) {
				defer wg.Done()
				err = s.runOneJob(phistage, job, run)
			}(job)
		}
		wg.Wait()

		if err != nil {
			logger.WithError(err).Errorf("[Stager runWithGraph] error occurred, skip following jobs")
			return err
		}

		// need to check this run's status
	}
	return nil
}

func (s *Stager) runOneJob(phistage *common.Phistage, job *common.Job, run *common.Run) error {
	logger := logrus.WithFields(logrus.Fields{"phistage": phistage.Name, "executor": phistage.Executor, "job": job.Name})

	jobRun := &common.JobRun{
		Phistage:  phistage.Name,
		Job:       job.Name,
		Status:    common.JobRunStatusPending,
		LogTracer: common.NewLogTracer(),
	}
	if err := s.store.CreateJobRun(context.TODO(), run, jobRun); err != nil {
		logger.WithError(err).Error("[Stager runOneJob] fail to create JobRun")
		return err
	}

	defer func() {
		if err := s.store.FinishJobRun(context.TODO(), run, jobRun); err != nil {
			logger.WithError(err).Errorf("[Stager runOneJob] error update JobRun")
		}
	}()

	// start JobRun
	jobRun.Start = time.Now()
	jobRun.Status = common.JobRunStatusRunning
	if err := s.store.UpdateJobRun(context.TODO(), run, jobRun); err != nil {
		logger.WithError(err).Errorf("[Stager runOneJob] error update JobRun")
		return err
	}

	executorProvider := executors.GetExecutorProvider(phistage.Executor)
	if executorProvider == nil {
		logger.Errorf("[Stager runOneJob] fail to get a provider")
		return errors.WithMessagef(executors.ErrorExecuteProviderNotFound, phistage.Name)
	}

	executor, err := executorProvider.GetJobExecutor(job, phistage, jobRun.LogTracer)
	if err != nil {
		logger.WithError(err).Errorf("[Stager runOneJob] fail to get a job executor")
		return err
	}

	defer func() {
		if err := executor.Cleanup(context.TODO()); err != nil {
			logger.WithError(err).Errorf("[Stager runOneJob] error when CLEANUP")
			return
		}
	}()

	if err := executor.Prepare(context.TODO()); err != nil {
		logger.WithError(err).Errorf("[Stager runOneJob] error when PREPARE")
		return err
	}

	if err := executor.Execute(context.TODO()); err != nil {
		logger.WithError(err).Errorf("[Stager runOneJob] error when EXECUTE")
		return err
	}

	return nil
}
