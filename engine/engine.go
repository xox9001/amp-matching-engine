package engine

import (
	"encoding/json"
	"errors"
	"log"
	"sync"

	"github.com/streadway/amqp"

	"github.com/Proofsuite/amp-matching-engine/rabbitmq"
	"github.com/Proofsuite/amp-matching-engine/redis"
	"github.com/Proofsuite/amp-matching-engine/types"
)

// Engine contains daos and redis connection required for engine to work
type Engine struct {
	redisConn *redis.RedisConnection
	mutex     *sync.Mutex
}

type EngineInterface interface {
	HandleOrders(msg *rabbitmq.Message) error
	SubscribeResponseQueue(fn func(*Response) error) error
	RecoverOrders(orders []*FillOrder) error
	CancelOrder(order *types.Order) (*Response, error)
	GetOrderBook(pair *types.Pair) (asks, bids []*map[string]float64)
}

// Engine is singleton Resource instance
var engine *Engine

// InitEngine initializes the engine singleton instance
func InitEngine(redisConn *redis.RedisConnection) (EngineInterface, error) {
	if engine == nil {
		engine = &Engine{redisConn, &sync.Mutex{}}
	}

	return engine, nil
}

// publishEngineResponse is used by matching engine to publish or send response of matching engine to
// system for further processing
func (e *Engine) publishEngineResponse(res *Response) error {
	ch := rabbitmq.GetChannel("erPub")
	q := rabbitmq.GetQueue(ch, "engineResponse")

	bytes, err := json.Marshal(res)
	if err != nil {
		log.Fatalf("Failed to marshal Engine Response: %s", err)
		return errors.New("Failed to marshal Engine Response: " + err.Error())
	}

	err = ch.Publish(
		"",     // exchange
		q.Name, // routing key
		false,  // mandatory
		false,  // immediate
		amqp.Publishing{
			ContentType: "text/json",
			Body:        bytes,
		},
	)

	if err != nil {
		log.Fatalf("Failed to publish order: %s", err)
		return errors.New("Failed to publish order: " + err.Error())
	}

	return nil
}

// SubscribeResponseQueue subscribes to engineResponse queue and triggers the function
// passed as arguments for each message.
func (e *Engine) SubscribeResponseQueue(fn func(*Response) error) error {
	ch := rabbitmq.GetChannel("erSub")
	q := rabbitmq.GetQueue(ch, "engineResponse")

	go func() {
		msgs, err := ch.Consume(
			q.Name, // queue
			"",     // consumer
			true,   // auto-ack
			false,  // exclusive
			false,  // no-local
			false,  // no-wait
			nil,    // args
		)

		if err != nil {
			log.Fatalf("Failed to register a consumer: %s", err)
		}

		forever := make(chan bool)

		go func() {
			for d := range msgs {
				var res *Response
				err := json.Unmarshal(d.Body, &res)
				if err != nil {
					log.Print(err)
					continue
				}
				go fn(res)
			}
		}()

		<-forever
	}()
	return nil
}

// HandleOrders parses incoming rabbitmq order messages and redirects them to the appropriate
// engine function
func (e *Engine) HandleOrders(msg *rabbitmq.Message) error {
	o := &types.Order{}
	err := json.Unmarshal(msg.Data, o)
	if err != nil {
		log.Print(err)
		return err
	}

	if msg.Type == "NEW_ORDER" {
		e.newOrder(o)
	} else if msg.Type == "ADD_ORDER" {
		e.addOrder(o)
	}

	return nil
}
