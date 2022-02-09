package coderd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"time"

	"github.com/go-chi/render"
	"github.com/google/uuid"
	"github.com/hashicorp/yamux"
	"github.com/moby/moby/pkg/namesgenerator"
	"golang.org/x/xerrors"
	"nhooyr.io/websocket"
	"storj.io/drpc/drpcmux"
	"storj.io/drpc/drpcserver"

	"cdr.dev/slog"

	"github.com/coder/coder/coderd/projectparameter"
	"github.com/coder/coder/database"
	"github.com/coder/coder/httpapi"
	"github.com/coder/coder/provisionerd/proto"
	sdkproto "github.com/coder/coder/provisionersdk/proto"
)

type ProvisionerDaemon database.ProvisionerDaemon

// Lists all registered provisioner daemons.
func (api *api) provisionerDaemons(rw http.ResponseWriter, r *http.Request) {
	daemons, err := api.Database.GetProvisionerDaemons(r.Context())
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	if err != nil {
		httpapi.Write(rw, http.StatusInternalServerError, httpapi.Response{
			Message: fmt.Sprintf("get provisioner daemons: %s", err),
		})
		return
	}
	if daemons == nil {
		daemons = []database.ProvisionerDaemon{}
	}
	render.Status(r, http.StatusOK)
	render.JSON(rw, r, daemons)
}

// Serves the provisioner daemon protobuf API over a WebSocket.
func (api *api) provisionerDaemonsServe(rw http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(rw, r, &websocket.AcceptOptions{
		// Need to disable compression to avoid a data-race.
		CompressionMode: websocket.CompressionDisabled,
	})
	if err != nil {
		httpapi.Write(rw, http.StatusBadRequest, httpapi.Response{
			Message: fmt.Sprintf("accept websocket: %s", err),
		})
		return
	}

	daemon, err := api.Database.InsertProvisionerDaemon(r.Context(), database.InsertProvisionerDaemonParams{
		ID:           uuid.New(),
		CreatedAt:    database.Now(),
		Name:         namesgenerator.GetRandomName(1),
		Provisioners: []database.ProvisionerType{database.ProvisionerTypeEcho, database.ProvisionerTypeTerraform},
	})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, fmt.Sprintf("insert provisioner daemon:% s", err))
		return
	}

	// Multiplexes the incoming connection using yamux.
	// This allows multiple function calls to occur over
	// the same connection.
	config := yamux.DefaultConfig()
	config.LogOutput = io.Discard
	session, err := yamux.Server(websocket.NetConn(r.Context(), conn, websocket.MessageBinary), config)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, fmt.Sprintf("multiplex server: %s", err))
		return
	}
	mux := drpcmux.New()
	err = proto.DRPCRegisterProvisionerDaemon(mux, &provisionerdServer{
		ID:           daemon.ID,
		Database:     api.Database,
		Pubsub:       api.Pubsub,
		Provisioners: daemon.Provisioners,
		Logger:       api.Logger.Named(fmt.Sprintf("provisionerd-%s", daemon.Name)),
	})
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, fmt.Sprintf("drpc register provisioner daemon: %s", err))
		return
	}
	server := drpcserver.New(mux)
	err = server.Serve(r.Context(), session)
	if err != nil {
		_ = conn.Close(websocket.StatusInternalError, fmt.Sprintf("serve: %s", err))
	}
}

// The input for a "workspace_provision" job.
type workspaceProvisionJob struct {
	WorkspaceHistoryID uuid.UUID `json:"workspace_history_id"`
	DryRun             bool      `json:"dry_run"`
}

// The input for a "project_import" job.
type projectVersionImportJob struct {
	OrganizationID string    `json:"organization_id"`
	ProjectID      uuid.UUID `json:"project_id"`

	AdditionalParameters []database.ParameterValue `json:"parameters"`
	SkipParameterSchemas bool                      `json:"skip_parameter_schemas"`
	SkipResources        bool                      `json:"skip_resources"`
}

// Implementation of the provisioner daemon protobuf server.
type provisionerdServer struct {
	ID           uuid.UUID
	Logger       slog.Logger
	Provisioners []database.ProvisionerType
	Database     database.Store
	Pubsub       database.Pubsub
}

// AcquireJob queries the database to lock a job.
func (server *provisionerdServer) AcquireJob(ctx context.Context, _ *proto.Empty) (*proto.AcquiredJob, error) {
	// This marks the job as locked in the database.
	job, err := server.Database.AcquireProvisionerJob(ctx, database.AcquireProvisionerJobParams{
		StartedAt: sql.NullTime{
			Time:  database.Now(),
			Valid: true,
		},
		WorkerID: uuid.NullUUID{
			UUID:  server.ID,
			Valid: true,
		},
		Types: server.Provisioners,
	})
	if errors.Is(err, sql.ErrNoRows) {
		// The provisioner daemon assumes no jobs are available if
		// an empty struct is returned.
		return &proto.AcquiredJob{}, nil
	}
	if err != nil {
		return nil, xerrors.Errorf("acquire job: %w", err)
	}
	server.Logger.Debug(ctx, "locked job from database", slog.F("id", job.ID))

	// Marks the acquired job as failed with the error message provided.
	failJob := func(errorMessage string) error {
		err = server.Database.UpdateProvisionerJobWithCompleteByID(ctx, database.UpdateProvisionerJobWithCompleteByIDParams{
			ID: job.ID,
			CompletedAt: sql.NullTime{
				Time:  database.Now(),
				Valid: true,
			},
			Error: sql.NullString{
				String: errorMessage,
				Valid:  true,
			},
		})
		if err != nil {
			return xerrors.Errorf("update provisioner job: %w", err)
		}
		return xerrors.Errorf("request job was invalidated: %s", errorMessage)
	}

	user, err := server.Database.GetUserByID(ctx, job.InitiatorID)
	if err != nil {
		return nil, failJob(fmt.Sprintf("get user: %s", err))
	}

	protoJob := &proto.AcquiredJob{
		JobId:       job.ID.String(),
		CreatedAt:   job.CreatedAt.UnixMilli(),
		Provisioner: string(job.Provisioner),
		UserName:    user.Username,
	}
	switch job.Type {
	case database.ProvisionerJobTypeWorkspaceProvision:
		var input workspaceProvisionJob
		err = json.Unmarshal(job.Input, &input)
		if err != nil {
			return nil, failJob(fmt.Sprintf("unmarshal job input %q: %s", job.Input, err))
		}
		workspaceHistory, err := server.Database.GetWorkspaceHistoryByID(ctx, input.WorkspaceHistoryID)
		if err != nil {
			return nil, failJob(fmt.Sprintf("get workspace history: %s", err))
		}
		workspace, err := server.Database.GetWorkspaceByID(ctx, workspaceHistory.WorkspaceID)
		if err != nil {
			return nil, failJob(fmt.Sprintf("get workspace: %s", err))
		}
		projectVersion, err := server.Database.GetProjectVersionByID(ctx, workspaceHistory.ProjectVersionID)
		if err != nil {
			return nil, failJob(fmt.Sprintf("get project version: %s", err))
		}
		project, err := server.Database.GetProjectByID(ctx, projectVersion.ProjectID)
		if err != nil {
			return nil, failJob(fmt.Sprintf("get project: %s", err))
		}
		organization, err := server.Database.GetOrganizationByID(ctx, project.OrganizationID)
		if err != nil {
			return nil, failJob(fmt.Sprintf("get organization: %s", err))
		}

		// Compute parameters for the workspace to consume.
		parameters, err := projectparameter.Compute(ctx, server.Database, projectparameter.Scope{
			ImportJobID:    projectVersion.ImportJobID,
			OrganizationID: organization.ID,
			ProjectID: uuid.NullUUID{
				UUID:  project.ID,
				Valid: true,
			},
			UserID: sql.NullString{
				String: user.ID,
				Valid:  true,
			},
			WorkspaceID: uuid.NullUUID{
				UUID:  workspace.ID,
				Valid: true,
			},
		})
		if err != nil {
			return nil, failJob(fmt.Sprintf("compute parameters: %s", err))
		}
		// Convert parameters to the protobuf type.
		protoParameters := make([]*sdkproto.ParameterValue, 0, len(parameters))
		for _, parameter := range parameters {
			protoParameters = append(protoParameters, parameter.Proto)
		}

		protoJob.Type = &proto.AcquiredJob_WorkspaceProvision_{
			WorkspaceProvision: &proto.AcquiredJob_WorkspaceProvision{
				WorkspaceHistoryId: workspaceHistory.ID.String(),
				WorkspaceName:      workspace.Name,
				State:              workspaceHistory.ProvisionerState,
				ParameterValues:    protoParameters,
			},
		}
	case database.ProvisionerJobTypeProjectVersionImport:
		var input projectVersionImportJob
		err = json.Unmarshal(job.Input, &input)
		if err != nil {
			return nil, failJob(fmt.Sprintf("unmarshal job input %q: %s", job.Input, err))
		}

		// Compute parameters for the workspace to consume.
		parameters, err := projectparameter.Compute(ctx, server.Database, projectparameter.Scope{
			ImportJobID:    job.ID,
			OrganizationID: input.OrganizationID,
			ProjectID: uuid.NullUUID{
				UUID:  input.ProjectID,
				Valid: input.ProjectID.String() != uuid.Nil.String(),
			},
			UserID: sql.NullString{
				String: user.ID,
				Valid:  true,
			},
		}, input.AdditionalParameters...)
		if err != nil {
			return nil, failJob(fmt.Sprintf("compute parameters: %s", err))
		}
		// Convert parameters to the protobuf type.
		protoParameters := make([]*sdkproto.ParameterValue, 0, len(parameters))
		for _, parameter := range parameters {
			protoParameters = append(protoParameters, parameter.Proto)
		}

		protoJob.Type = &proto.AcquiredJob_ProjectImport_{
			ProjectImport: &proto.AcquiredJob_ProjectImport{
				ParameterValues:      protoParameters,
				SkipParameterSchemas: input.SkipParameterSchemas,
				SkipResources:        input.SkipResources,
			},
		}
	}
	switch job.StorageMethod {
	case database.ProvisionerStorageMethodFile:
		file, err := server.Database.GetFileByHash(ctx, job.StorageSource)
		if err != nil {
			return nil, failJob(fmt.Sprintf("get file by hash: %s", err))
		}
		protoJob.ProjectSourceArchive = file.Data
	default:
		return nil, failJob(fmt.Sprintf("unsupported storage method: %s", job.StorageMethod))
	}

	return protoJob, err
}

func (server *provisionerdServer) UpdateJob(stream proto.DRPCProvisionerDaemon_UpdateJobStream) error {
	for {
		update, err := stream.Recv()
		if err != nil {
			return err
		}
		parsedID, err := uuid.Parse(update.JobId)
		if err != nil {
			return xerrors.Errorf("parse job id: %w", err)
		}
		job, err := server.Database.GetProvisionerJobByID(stream.Context(), parsedID)
		if err != nil {
			return xerrors.Errorf("get job: %w", err)
		}
		if !job.WorkerID.Valid {
			return xerrors.New("job isn't running yet")
		}
		if job.WorkerID.UUID.String() != server.ID.String() {
			return xerrors.New("you don't own this job")
		}

		err = server.Database.UpdateProvisionerJobByID(stream.Context(), database.UpdateProvisionerJobByIDParams{
			ID:        parsedID,
			UpdatedAt: database.Now(),
		})
		if err != nil {
			return xerrors.Errorf("update job: %w", err)
		}
		insertParams := database.InsertProvisionerJobLogsParams{
			JobID: parsedID,
		}
		for _, log := range update.Logs {
			logLevel, err := convertLogLevel(log.Level)
			if err != nil {
				return xerrors.Errorf("convert log level: %w", err)
			}
			logSource, err := convertLogSource(log.Source)
			if err != nil {
				return xerrors.Errorf("convert log source: %w", err)
			}
			insertParams.ID = append(insertParams.ID, uuid.New())
			insertParams.CreatedAt = append(insertParams.CreatedAt, time.UnixMilli(log.CreatedAt))
			insertParams.Level = append(insertParams.Level, logLevel)
			insertParams.Source = append(insertParams.Source, logSource)
			insertParams.Output = append(insertParams.Output, log.Output)
		}
		logs, err := server.Database.InsertProvisionerJobLogs(context.Background(), insertParams)
		if err != nil {
			return xerrors.Errorf("insert job logs: %w", err)
		}
		data, err := json.Marshal(logs)
		if err != nil {
			return xerrors.Errorf("marshal job log: %w", err)
		}
		err = server.Pubsub.Publish(provisionerJobLogsChannel(parsedID), data)
		if err != nil {
			return xerrors.Errorf("publish job log: %w", err)
		}
	}
}

func (server *provisionerdServer) CancelJob(ctx context.Context, cancelJob *proto.CancelledJob) (*proto.Empty, error) {
	jobID, err := uuid.Parse(cancelJob.JobId)
	if err != nil {
		return nil, xerrors.Errorf("parse job id: %w", err)
	}
	job, err := server.Database.GetProvisionerJobByID(ctx, jobID)
	if err != nil {
		return nil, xerrors.Errorf("get provisioner job: %w", err)
	}
	if job.CompletedAt.Valid {
		return nil, xerrors.Errorf("job already completed")
	}
	err = server.Database.UpdateProvisionerJobWithCompleteByID(ctx, database.UpdateProvisionerJobWithCompleteByIDParams{
		ID: jobID,
		CompletedAt: sql.NullTime{
			Time:  database.Now(),
			Valid: true,
		},
		CancelledAt: sql.NullTime{
			Time:  database.Now(),
			Valid: true,
		},
		UpdatedAt: database.Now(),
		Error: sql.NullString{
			String: cancelJob.Error,
			Valid:  cancelJob.Error != "",
		},
	})
	if err != nil {
		return nil, xerrors.Errorf("update provisioner job: %w", err)
	}
	return &proto.Empty{}, nil
}

// CompleteJob is triggered by a provision daemon to mark a provisioner job as completed.
func (server *provisionerdServer) CompleteJob(ctx context.Context, completed *proto.CompletedJob) (*proto.Empty, error) {
	jobID, err := uuid.Parse(completed.JobId)
	if err != nil {
		return nil, xerrors.Errorf("parse job id: %w", err)
	}
	job, err := server.Database.GetProvisionerJobByID(ctx, jobID)
	if err != nil {
		return nil, xerrors.Errorf("get job by id: %w", err)
	}
	// TODO: Check if the worker ID matches!
	// If it doesn't, a provisioner daemon could be impersonating another job!

	switch jobType := completed.Type.(type) {
	case *proto.CompletedJob_ProjectImport_:
		var input projectVersionImportJob
		err = json.Unmarshal(job.Input, &input)
		if err != nil {
			return nil, xerrors.Errorf("unmarshal job data: %w", err)
		}

		// Validate that all parameters send from the provisioner daemon
		// follow the protocol.
		parameterSchemas := make([]database.InsertParameterSchemaParams, 0, len(jobType.ProjectImport.ParameterSchemas))
		for _, protoParameter := range jobType.ProjectImport.ParameterSchemas {
			validationTypeSystem, err := convertValidationTypeSystem(protoParameter.ValidationTypeSystem)
			if err != nil {
				return nil, xerrors.Errorf("convert validation type system for %q: %w", protoParameter.Name, err)
			}

			parameterSchema := database.InsertParameterSchemaParams{
				ID:                   uuid.New(),
				CreatedAt:            database.Now(),
				JobID:                job.ID,
				Name:                 protoParameter.Name,
				Description:          protoParameter.Description,
				RedisplayValue:       protoParameter.RedisplayValue,
				ValidationError:      protoParameter.ValidationError,
				ValidationCondition:  protoParameter.ValidationCondition,
				ValidationValueType:  protoParameter.ValidationValueType,
				ValidationTypeSystem: validationTypeSystem,

				DefaultSourceScheme:      database.ParameterSourceSchemeNone,
				DefaultDestinationScheme: database.ParameterDestinationSchemeNone,

				AllowOverrideDestination: protoParameter.AllowOverrideDestination,
				AllowOverrideSource:      protoParameter.AllowOverrideSource,
			}

			// It's possible a parameter doesn't define a default source!
			if protoParameter.DefaultSource != nil {
				parameterSourceScheme, err := convertParameterSourceScheme(protoParameter.DefaultSource.Scheme)
				if err != nil {
					return nil, xerrors.Errorf("convert parameter source scheme: %w", err)
				}
				parameterSchema.DefaultSourceScheme = parameterSourceScheme
				parameterSchema.DefaultSourceValue = sql.NullString{
					String: protoParameter.DefaultSource.Value,
					Valid:  protoParameter.DefaultSource.Value != "",
				}
			}

			// It's possible a parameter doesn't define a default destination!
			if protoParameter.DefaultDestination != nil {
				parameterDestinationScheme, err := convertParameterDestinationScheme(protoParameter.DefaultDestination.Scheme)
				if err != nil {
					return nil, xerrors.Errorf("convert parameter destination scheme: %w", err)
				}
				parameterSchema.DefaultDestinationScheme = parameterDestinationScheme
				parameterSchema.DefaultDestinationValue = sql.NullString{
					String: protoParameter.DefaultDestination.Value,
					Valid:  protoParameter.DefaultDestination.Value != "",
				}
			}

			parameterSchemas = append(parameterSchemas, parameterSchema)
		}

		// This must occur in a transaction in case of failure.
		err = server.Database.InTx(func(db database.Store) error {
			err = db.UpdateProvisionerJobWithCompleteByID(ctx, database.UpdateProvisionerJobWithCompleteByIDParams{
				ID:        jobID,
				UpdatedAt: database.Now(),
				CompletedAt: sql.NullTime{
					Time:  database.Now(),
					Valid: true,
				},
			})
			if err != nil {
				return xerrors.Errorf("update provisioner job: %w", err)
			}
			// This could be a bulk-insert operation to improve performance.
			// See the "InsertWorkspaceHistoryLogs" query.
			for _, parameterSchema := range parameterSchemas {
				_, err = db.InsertParameterSchema(ctx, parameterSchema)
				if err != nil {
					return xerrors.Errorf("insert parameter schema %q: %w", parameterSchema.Name, err)
				}
			}
			server.Logger.Debug(ctx, "marked import job as completed", slog.F("job_id", jobID))
			return nil
		})
		if err != nil {
			return nil, xerrors.Errorf("complete job: %w", err)
		}
	case *proto.CompletedJob_WorkspaceProvision_:
		var input workspaceProvisionJob
		err = json.Unmarshal(job.Input, &input)
		if err != nil {
			return nil, xerrors.Errorf("unmarshal job data: %w", err)
		}

		workspaceHistory, err := server.Database.GetWorkspaceHistoryByID(ctx, input.WorkspaceHistoryID)
		if err != nil {
			return nil, xerrors.Errorf("get workspace history: %w", err)
		}

		err = server.Database.InTx(func(db database.Store) error {
			err = db.UpdateProvisionerJobWithCompleteByID(ctx, database.UpdateProvisionerJobWithCompleteByIDParams{
				ID:        jobID,
				UpdatedAt: database.Now(),
				CompletedAt: sql.NullTime{
					Time:  database.Now(),
					Valid: true,
				},
			})
			if err != nil {
				return xerrors.Errorf("update provisioner job: %w", err)
			}
			err = db.UpdateWorkspaceHistoryByID(ctx, database.UpdateWorkspaceHistoryByIDParams{
				ID:               workspaceHistory.ID,
				UpdatedAt:        database.Now(),
				ProvisionerState: jobType.WorkspaceProvision.State,
			})
			if err != nil {
				return xerrors.Errorf("update workspace history: %w", err)
			}
			// This could be a bulk insert to improve performance.
			for _, protoResource := range jobType.WorkspaceProvision.Resources {
				_, err = db.InsertWorkspaceResource(ctx, database.InsertWorkspaceResourceParams{
					ID:                 uuid.New(),
					CreatedAt:          database.Now(),
					WorkspaceHistoryID: input.WorkspaceHistoryID,
					Type:               protoResource.Type,
					Name:               protoResource.Name,
					// TODO: Generate this at the variable validation phase.
					// Set the value in `default_source`, and disallow overwrite.
					WorkspaceAgentToken: uuid.NewString(),
				})
				if err != nil {
					return xerrors.Errorf("insert workspace resource %q: %w", protoResource.Name, err)
				}
			}
			return nil
		})
		if err != nil {
			return nil, xerrors.Errorf("complete job: %w", err)
		}
	default:
		return nil, xerrors.Errorf("unknown job type %q; ensure coderd and provisionerd versions match",
			reflect.TypeOf(completed.Type).String())
	}

	return &proto.Empty{}, nil
}

func convertValidationTypeSystem(typeSystem sdkproto.ParameterSchema_TypeSystem) (database.ParameterTypeSystem, error) {
	switch typeSystem {
	case sdkproto.ParameterSchema_None:
		return database.ParameterTypeSystemNone, nil
	case sdkproto.ParameterSchema_HCL:
		return database.ParameterTypeSystemHCL, nil
	default:
		return database.ParameterTypeSystem(""), xerrors.Errorf("unknown type system: %d", typeSystem)
	}
}

func convertParameterSourceScheme(sourceScheme sdkproto.ParameterSource_Scheme) (database.ParameterSourceScheme, error) {
	switch sourceScheme {
	case sdkproto.ParameterSource_DATA:
		return database.ParameterSourceSchemeData, nil
	default:
		return database.ParameterSourceScheme(""), xerrors.Errorf("unknown parameter source scheme: %d", sourceScheme)
	}
}

func convertParameterDestinationScheme(destinationScheme sdkproto.ParameterDestination_Scheme) (database.ParameterDestinationScheme, error) {
	switch destinationScheme {
	case sdkproto.ParameterDestination_ENVIRONMENT_VARIABLE:
		return database.ParameterDestinationSchemeEnvironmentVariable, nil
	case sdkproto.ParameterDestination_PROVISIONER_VARIABLE:
		return database.ParameterDestinationSchemeProvisionerVariable, nil
	default:
		return database.ParameterDestinationScheme(""), xerrors.Errorf("unknown parameter destination scheme: %d", destinationScheme)
	}
}

func convertLogLevel(logLevel sdkproto.LogLevel) (database.LogLevel, error) {
	switch logLevel {
	case sdkproto.LogLevel_TRACE:
		return database.LogLevelTrace, nil
	case sdkproto.LogLevel_DEBUG:
		return database.LogLevelDebug, nil
	case sdkproto.LogLevel_INFO:
		return database.LogLevelInfo, nil
	case sdkproto.LogLevel_WARN:
		return database.LogLevelWarn, nil
	case sdkproto.LogLevel_ERROR:
		return database.LogLevelError, nil
	default:
		return database.LogLevel(""), xerrors.Errorf("unknown log level: %d", logLevel)
	}
}

func convertLogSource(logSource proto.LogSource) (database.LogSource, error) {
	switch logSource {
	case proto.LogSource_PROVISIONER_DAEMON:
		return database.LogSourceProvisionerDaemon, nil
	case proto.LogSource_PROVISIONER:
		return database.LogSourceProvisioner, nil
	default:
		return database.LogSource(""), xerrors.Errorf("unknown log source: %d", logSource)
	}
}