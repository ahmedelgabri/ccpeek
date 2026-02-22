#!/bin/zsh
export PATH="/usr/local/go/bin:/usr/local/bin:$HOME/go/bin:$PATH"
export GOPATH="$HOME/go"
export EDITOR="nvim"
export PAGER="less"
export LANG="en_US.UTF-8"

alias ll="ls -la"
alias gs="git status"
alias gd="git diff"
alias gc="git commit"
alias gp="git push"

# Node version manager
export NVM_DIR="$HOME/.nvm"
[ -s "$NVM_DIR/nvm.sh" ] && \. "$NVM_DIR/nvm.sh"
