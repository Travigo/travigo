package nrod

type TrustActivation struct {
	EventType  string `json:"event_type"`
	TrainID    string `json:"train_id"`
	OperatorID string `json:"toc_id"`

	ScheduleSource              string `json:"schedule_source"`
	TrainFileAddress            string `json:"train_file_address"`
	TrainUID                    string `json:"train_uid"`
	CreationTimestamp           string `json:"creation_timestamp"`
	TrainPlannedOriginTimestamp string `json:"tp_origin_timestamp"`
	TrainPlannedOriginStanox    string `json:"tp_origin_stanox"`
	OriginDepartureTimestamp    string `json:"origin_dep_timestamp"`
	TrainServiceCode            string `json:"train_service_code"`
	D1266RecordNumber           string `json:"d1266_record_number"`
	TrainCallType               string `json:"train_call_type"`
	TrainCallMode               string `json:"train_call_mode"`
	ScheduleType                string `json:"schedule_type"`
	ScheduleOriginStanox        string `json:"sched_origin_stanox"`
	ScheduleWorkingTimetableID  string `json:"schedule_wtt_id"`
	ScheduleStartDate           string `json:"schedule_start_date"`
	ScheduleEndDate             string `json:"schedule_end_date"`
}
