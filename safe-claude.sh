#!/bin/bash

WORKTREE_DIR="/Users/paul/go-projects/cyoda-go/cyoda-go-m1"
CYODA_MAIN="/Users/paul/dev/cyoda"

echo "Booting Claude in Safehouse for M1..."

safehouse \
  --add-dirs "$WORKTREE_DIR" \
  --add-dirs-ro "$CYODA_MAIN" \
  -- claude  "$@" 
