package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"
	"zohoclient/entity"
	"zohoclient/internal/config"
	"zohoclient/internal/lib/sl"

	"go.mongodb.org/mongo-driver/bson"
	"go.mongodb.org/mongo-driver/mongo"
	"go.mongodb.org/mongo-driver/mongo/options"
)

const (
	ordersCollection = "orders"
)

type MongoDB struct {
	ctx           context.Context
	clientOptions *options.ClientOptions
	database      string
	expiredDays   int
	log           *slog.Logger
}

func NewMongoClient(conf *config.Config, logger *slog.Logger) (*MongoDB, error) {
	if !conf.Mongo.Enabled {
		return nil, nil
	}
	connectionUri := fmt.Sprintf("mongodb://%s:%s", conf.Mongo.Host, conf.Mongo.Port)
	clientOptions := options.Client().ApplyURI(connectionUri)
	if conf.Mongo.User != "" {
		clientOptions.SetAuth(options.Credential{
			Username:   conf.Mongo.User,
			Password:   conf.Mongo.Password,
			AuthSource: conf.Mongo.Database,
		})
	}
	client := &MongoDB{
		ctx:           context.Background(),
		clientOptions: clientOptions,
		database:      conf.Mongo.Database,
		expiredDays:   conf.Mongo.ExpiredDays,
		log:           logger.With(sl.Module("mongodb")),
	}
	return client, nil
}

func (m *MongoDB) connect() (*mongo.Client, error) {
	connection, err := mongo.Connect(m.ctx, m.clientOptions)
	if err != nil {
		return nil, fmt.Errorf("mongodb connect error: %w", err)
	}
	return connection, nil
}

func (m *MongoDB) disconnect(connection *mongo.Client) {
	_ = connection.Disconnect(m.ctx)
}

func (m *MongoDB) findError(err error) error {
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil
	}
	return fmt.Errorf("mongodb find error: %w", err)
}

// SaveOrderVersion saves or updates an order with a new version in MongoDB.
// If the order exists, appends the new version. If not, creates a new order document.
// Version ID is auto-generated as sequential number (0, 1, 2, ...).
func (m *MongoDB) SaveOrderVersion(orderID int64, payload string) error {
	connection, err := m.connect()
	if err != nil {
		return err
	}
	defer m.disconnect(connection)

	collection := connection.Database(m.database).Collection(ordersCollection)

	// Try to find existing order
	filter := bson.M{"order_id": orderID}
	var existingOrder entity.MongoOrder
	err = collection.FindOne(m.ctx, filter).Decode(&existingOrder)

	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			// Create new order document with version 0
			version := entity.Version{ID: "0", Payload: payload}
			newOrder := entity.MongoOrder{
				CreationDate: time.Now(),
				OrderID:      orderID,
				Versions:     []entity.Version{version},
			}
			_, err = collection.InsertOne(m.ctx, newOrder)
			if err != nil {
				return fmt.Errorf("mongodb insert error: %w", err)
			}
			m.log.Debug("created new order in mongodb", slog.Int64("order_id", orderID), slog.String("version_id", "0"))
			return nil
		}
		return m.findError(err)
	}

	// Order exists, append new version with next sequential ID
	nextID := fmt.Sprintf("%d", len(existingOrder.Versions))
	version := entity.Version{ID: nextID, Payload: payload}
	update := bson.M{
		"$push": bson.M{"versions": version},
	}
	_, err = collection.UpdateOne(m.ctx, filter, update)
	if err != nil {
		return fmt.Errorf("mongodb update error: %w", err)
	}

	m.log.Debug("added version to order in mongodb", slog.Int64("order_id", orderID), slog.String("version_id", nextID))
	return nil
}

// DeleteExpired removes order documents older than expiredDays from MongoDB.
// Returns the number of deleted documents.
func (m *MongoDB) DeleteExpired() (int64, error) {
	if m.expiredDays <= 0 {
		return 0, nil
	}

	connection, err := m.connect()
	if err != nil {
		return 0, err
	}
	defer m.disconnect(connection)

	collection := connection.Database(m.database).Collection(ordersCollection)

	cutoffDate := time.Now().AddDate(0, 0, -m.expiredDays)
	filter := bson.M{"creation_date": bson.M{"$lt": cutoffDate}}

	result, err := collection.DeleteMany(m.ctx, filter)
	if err != nil {
		return 0, fmt.Errorf("mongodb delete error: %w", err)
	}

	if result.DeletedCount > 0 {
		m.log.Info("deleted expired orders from mongodb",
			slog.Int64("deleted_count", result.DeletedCount),
			slog.Int("expired_days", m.expiredDays))
	}

	return result.DeletedCount, nil
}
