# First primitive: bounded subprocess execution

Reusery's first primitive is intentionally unpleasant.

A subprocess runner looks trivial until its contract includes bounded stdout and stderr, cancellation, timeouts, pipe draining, exit semantics, grandchildren and platform differences. That makes it useful for testing whether Reusery can distinguish a merely relevant implementation from one that actually satisfies an engineering need.

## Why this primitive

It exposes several failure modes that ordinary code search can miss:

- reading stdout fully before stderr can deadlock when the other pipe fills;
- capturing output without a bound permits memory growth controlled by a child process;
- killing only the direct child may leave descendants alive;
- Unix process groups and Windows process-tree mechanisms are not identical;
- a timeout, cancellation, launch error and non-zero process exit are different events;
- cleanup logic can itself leak goroutines, threads, descriptors or pipes.

The initial contract therefore makes these behaviours explicit rather than assuming that a function named `Run` or `Command` has them.

## What success looks like

The first resolver slice should be able to inspect candidate implementations and say which requirements have evidence, which fail, and which remain unknown.

It must be allowed to conclude **BUILD LOCALLY** when no discovered specimen satisfies the contract economically.

No candidate gets credit merely because Reusery failed to find contrary evidence.
