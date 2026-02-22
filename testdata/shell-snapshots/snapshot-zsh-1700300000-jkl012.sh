#!/bin/zsh
export PATH="$HOME/.local/bin:/opt/homebrew/bin:/usr/local/bin:$PATH"
export EDITOR="vim"
export VISUAL="vim"
export GPG_TTY=$(tty)

# Kubernetes
export KUBECONFIG="$HOME/.kube/config"
alias k="kubectl"
alias kgp="kubectl get pods"
alias kgs="kubectl get svc"
alias kga="kubectl get all"

# Docker
alias dc="docker compose"
alias dps="docker ps"

# Terraform
alias tf="terraform"
alias tfi="terraform init"
alias tfp="terraform plan"
alias tfa="terraform apply"
