# Copyright 2016 The Kubernetes Authors.
#
# Licensed under the Apache License, Version 2.0 (the "License");
# you may not use this file except in compliance with the License.
# You may obtain a copy of the License at
#
#     http://www.apache.org/licenses/LICENSE-2.0
#
# Unless required by applicable law or agreed to in writing, software
# distributed under the License is distributed on an "AS IS" BASIS,
# WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
# See the License for the specific language governing permissions and
# limitations under the License.

# This Dockerfile will build an image that is configured
# to use Fluentd to collect all Docker container log files
# and then cause them to be ingested using the Google Cloud
# Logging API. This configuration assumes that the host performning
# the collection is a VM that has been created with a logging.write
# scope and that the Logging API has been enabled for the project
# in the Google Developer Console.

FROM gcr.io/google_containers/ubuntu-slim:0.4

MAINTAINER Mik Vyatskov "vmik@google.com"

# Disable prompts from apt
ENV DEBIAN_FRONTEND noninteractive

# Install build tools
RUN apt-get -qq update && \
    apt-get install -y -qq curl ca-certificates bash sudo && \
    apt-get install -y -qq --reinstall lsb-base lsb-release && \
    # Install logging agent and required gems
    curl -sSL https://dl.google.com/cloudagents/install-logging-agent.sh | sudo bash && \
    google-fluentd-gem install --no-document fluent-plugin-record-reformer -v 0.8.2 && \
    google-fluentd-gem install --no-document fluent-plugin-systemd -v 0.0.5 && \
    # Remove build tools
    apt-get autoremove -y -qq && \
    apt-get clean -qq && \
    # Remove unnecessary files
    rm -rf /var/lib/apt/lists/* /tmp/* /var/tmp/* \
        /opt/google-fluentd/embedded/share/doc \
        /opt/google-fluentd/embedded/share/gtk-doc \
        /opt/google-fluentd/embedded/lib/postgresql \
        /opt/google-fluentd/embedded/bin/postgres \
        /opt/google-fluentd/embedded/share/postgresql \
        /etc/google-fluentd/google-fluentd.conf

# Copy the Fluentd configuration file for logging Docker container logs.
COPY google-fluentd.conf /etc/google-fluentd/google-fluentd.conf

# Copy the entrypoint for the container
COPY run.sh /run.sh

# Start Fluentd to pick up our config that watches Docker container logs.
CMD /run.sh $FLUENTD_ARGS
