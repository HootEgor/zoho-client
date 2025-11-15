# Zoho Client

![Go](https://img.shields.io/badge/Go-1.22-blue.svg?logo=go)
![MySQL](https://img.shields.io/badge/MySQL-8.0-blue.svg?logo=mysql)
![Telegram](https://img.shields.io/badge/Telegram-Bot%20API-blue.svg?logo=telegram)

This project is a client for the Zoho API, with Telegram bot integration and MySQL database support.

## Features

*   Interacts with the Zoho API
*   Telegram bot for notifications and commands
*   Uses MySQL for data storage
*   Manages a product repository

## Getting Started

1.  Clone the repository:
    ```bash
    git clone https://github.com/your-username/zohoclient.git
    ```
2.  Install dependencies:
    ```bash
    go mod download
    ```
3.  Configure the application by creating a `config.yml` file. You can use `config.example.yml` as a template.
4.  Build and run the application:
    ```bash
    go run cmd/zoho/main.go
    ```
