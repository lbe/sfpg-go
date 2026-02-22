# package queue

A double-ended queue using a resizable ring buffer as its store

## Data structure

- Generalize queue and stack
- Efficiently add and/or remove items at either end of the with O(1) performance
- Support queue operations (FIFO) with `enqueue` add to the back and `dequeue` to remove from the front.
- Support stack operations (LIFO) with `push` and `pop`. `push` and `enqueue` are the same action.
- Optimize implementation for CPU and GC performance.
- Resize the circular buffer automatically by powers of two
- Grow when additional capacity is needed
- Shrink when only a quarter of the capacity is used
- Use bitwise arithmetic for all calculations. Since growth is by powers of two, adding elements will only cause O(log n) allocations.
- Wrap around the buffer to reuse previously used space making allocation unnecessary until all buffer capacity is used.
- Support concurrent use. Use the fastest sync primitives possible.
- Use only stdlib in the construction of this package.
- Require the user to specify the type upon creating the queue.
- Use generics to make sure that all code is type safe.

## General Build

- Make sure that all code provide will build properly before sharing it
- Include tests to make sure that all code paths in all functions are tested.
