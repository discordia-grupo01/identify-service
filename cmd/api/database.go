package main

import (
	"database/sql"
	"fmt"
	"log"
	"time"
)

func initDB() *sql.DB {
	dbHost := getEnv("DATABASE_HOST", "localhost")
	dbPort := getEnv("DATABASE_PORT", "5432")
	dbUser := getEnv("DATABASE_USER", "discordia")
	dbPass := getEnv("DATABASE_PASSWORD", "discordia")
	dbName := getEnv("DATABASE_NAME", "discordia_servicio")
	dbSSLMode := getEnv("DATABASE_SSLMODE", "disable")
	dbSchema := getEnv("DATABASE_SCHEMA", "")

	dsn := fmt.Sprintf(
		"host=%s port=%s user=%s password=%s dbname=%s sslmode=%s",
		dbHost, dbPort, dbUser, dbPass, dbName, dbSSLMode,
	)
	if dbSchema != "" {
		// Fija el search_path de la sesion al schema del servicio (ej. "identify"
		// en el proyecto de Supabase compartido). 
		dsn += fmt.Sprintf(" options='-c search_path=%s'", dbSchema)
	}

	db, err := sql.Open("postgres", dsn)
	if err != nil {
		log.Fatalf("Error crítico al abrir driver de Postgres: %v", err)
	}

	// Configuración del pool de conexiones
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	// Reintentos de conexión para tolerancia a fallas en entornos Docker
	var pingErr error
	for i := 1; i <= 5; i++ {
		pingErr = db.Ping()
		if pingErr == nil {
			log.Println("Conexión exitosa a PostgreSQL")
			return db
		}
		log.Printf("Esperando a PostgreSQL (intento %d/5)... error: %v", i, pingErr)
		time.Sleep(2 * time.Second)
	}

	log.Fatalf("No se pudo conectar a PostgreSQL tras 5 intentos: %v", pingErr)
	return nil
}
