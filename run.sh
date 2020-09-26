#!/bin/sh

cp /bin/kubexit /kubexit/
# We're creating the graveyard folder for kubexit
# that's going to be shared among the containers in the pod
# to signal each other's status.
mkdir -p /kubexit/graveyard
