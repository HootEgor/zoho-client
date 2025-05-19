# OCAPI Project

## Overview
OCAPI (OpenCart API) is a project designed to provide a robust and flexible API for the OpenCart site database. It allows users to update site products and retrieve orders seamlessly.

## Features
- Product management
- Order retrieval (coming soon)

## Prerequisites
- Go 1.16 or higher
- Installed OpenCart site

The project includes GitHub actions scripts for CI/CD, you can use as a template for your environment.

## Setup

### Clone the repository
Clone repository to your local machine or server, build application, and deploy it alongside your OpenCart site.

### Configure the database
Update configuration file, default is the `config.yml` file, with your OpenCart database credentials. Provide port, on which the OCAPI service will run, and the API key.
- [Configuration file structure](docs/config.md)

### Run the application
Run the application manually or set up a service to run it in the background.
Command line parameters:
- `-conf` - path to the configuration file, default `config.yml`
- `-log` - path to the log file `ocapi.log`, default `/var/log/`
Example:
```shell
/usr/local/bin/ocapi -conf=/etc/conf/config.yml -log=/var/log/
```

### API Documentation
- [API v1](docs/apiv1.md)

## License
This project is licensed under the MIT License. See the [LICENSE](LICENSE) file for details.

## Contact
For any questions or inquiries, please contact developer at [dev@nomadus.net](mailto:dev@nomadus.net).
