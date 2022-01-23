package rabbitmq

import (
	"github.com/britbus/britbus/pkg/util"
	"github.com/streadway/amqp"
)

type RabbitMQInstance struct {
	Connection *amqp.Connection
}

var rabbitMQ RabbitMQInstance

const defaultConnectionString = "amqp://guest:guest@localhost:5672/"

func Connect() error {
	connectionString := defaultConnectionString

	env := util.GetEnvironmentVariables()

	if env["BRITBUS_RABBITMQ_CONNECTION"] != "" {
		connectionString = env["BRITBUS_RABBITMQ_CONNECTION"]
	}

	connection, err := amqp.Dial(connectionString)
	if err != nil {
		return err
	}
	// defer connection.Close()

	rabbitMQ = RabbitMQInstance{
		Connection: connection,
	}

	return nil
}

func GetChannel() (*amqp.Channel, error) {
	channel, err := rabbitMQ.Connection.Channel()
	if err != nil {
		return nil, err
	}

	return channel, nil
}
