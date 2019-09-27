[![Build Status](https://travis-ci.com/joomcode/errorx.svg?branch=master)](https://travis-ci.com/joomcode/errorx)
[![GoDoc](https://godoc.org/github.com/joomcode/errorx?status.svg)](https://godoc.org/github.com/joomcode/errorx)
[![Report Card](https://goreportcard.com/badge/github.com/joomcode/errorx)](https://goreportcard.com/report/github.com/joomcode/errorx)
[![gocover.io](https://gocover.io/_badge/github.com/joomcode/errorx)](https://gocover.io/github.com/joomcode/errorx)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go#miscellaneous)

## Highlights

The *errorx* library provides error implementation and error-related utilities. 
Library features include (but are not limited to):
* Stack traces 
* Composability of errors
* Means to enhance error both with stack trace and with message
* Robust type and trait checks

## Introduction

Conventional approach towards errors in *Go* is quite limited.

The typical case implies an error being created at some point:
```go
return errors.New("now this is unfortunate")
```

Then being passed along with a no-brainer:
```go
if err != nil {
  return err
}
```

And, finally, handled by printing it to the log file:
```go
log.Printf("Error: %s", err)
```

It doesn't take long to find out that quite often this is not enough. There's little fun in solving the issue when everything a developer is able to observe is a line in the log that looks like one of those:
> Error: EOF

> Error: unexpected '>' at the beginning of value

> Error: wrong argument value

An *errorx* library makes an approach to create a toolset that would help remedy this issue with these considerations in mind:
* No extra care should be required for an error to have all the necessary debug information; it is the opposite that may constitute a special case
* There must be a way to distinguish one kind of error from another, as they may imply or require a different handling in user code
* Errors must be composable, and patterns like ```if err == io.EOF``` defeat that purpose, so they should be avoided
* Some context information may be added to the error along the way, and there must be a way to do so without altering the semantics of the error
* It must be easy to create an error, add some context to it, check for it
* A kind of error that requires a special treatment by the caller *is* a part of a public API; an excessive amount of such kinds is a code smell

As a result, the goal of the library is to provide a brief, expressive syntax for a conventional error handling and to discourage usage patterns that bring more harm than they're worth.

Error-related, negative codepath is typically less well tested, though of, and may confuse the reader more than its positive counterpart. Therefore, an error system could do well without too much of a flexibility and unpredictability.

# errorx

With *errorx*, the pattern above looks like this:

```go
return errorx.IllegalState.New("unfortunate")
```
```go
if err != nil {
  return errorx.Decorate(err, "this could be so much better")
}
```
```go
log.Printf("Error: %+v", err)
```

An error message will look something like this:

```
Error: this could be so much better, cause: common.illegal_state: unfortunate
 at main.culprit()
	main.go:21
 at main.innocent()
	main.go:16
 at main.main()
	main.go:11
  ```

Now we have some context to our little problem, as well as a full stack trace of the original cause - which is, in effect, all that you really need, most of the time. ```errorx.Decorate``` is handy to add some info which a stack trace does not already hold: an id of the relevant entity, a portion of the failed request, etc. In all other cases, the good old ```if err != nil {return err}``` still works for you.

And this, frankly, may be quite enough. With a set of standard error types provided with *errorx* and a syntax to create your own (note that a name of the type is a good way to express its semantics), the best way to deal with errors is in an opaque manner: create them, add information and log as some point. Whenever this is sufficient, don't go any further. The simpler, the better.

## Error check

If an error requires special treatment, it may be done like this:
```go
// MyError = MyErrors.NewType("my_error")
if errorx.IsOfType(err, MyError) {
  // handle
}
```

Note that it is never a good idea to inspect a message of an error. Type check, on the other hand, is sometimes OK, especially if this technique is used inside of a package rather than forced upon API users.

An alternative is a mechanisms called **traits**:
```go
// the first parameter is a name of new error type, the second is a reference to existing trait
TimeoutElapsed       = MyErrors.NewType("timeout", errorx.Timeout())
```

Here, ```TimeoutElapsed``` error type is created with a Timeout() trait, and errors may be checked against it:
```go
if errorx.HasTrait(err, errorx.Timeout()) {
  // handle
}
```

Note that here a check is made against a trait, not a type, so any type with the same trait would pass it. Type check is more restricted this way and creates tighter dependency if used outside of an originating package. It allows for some little flexibility, though: via a subtype feature a broader type check can be made.

## Wrap

The example above introduced ```errorx.Decorate()```, a syntax used to add message as an error is passed along. This mechanism is highly non-intrusive: any properties an original error possessed, a result of a  ```Decorate()``` will possess, too.

Sometimes, though, it is not the desired effect. A possibility to make a type check is a double edged one, and should be restricted as often as it is allowed. The bad way to do so would be to create a new error and to pass an ```Error()``` output as a message. Among other possible issues, this would either lose or duplicate the stack trace information.

A better alternative is:
```go
return MyError.Wrap(err, "fail")
```

With ```Wrap()```, an original error is fully retained for the log, but hidden from type checks by the caller.

See ```WrapMany()``` and ```DecorateMany()``` for more sophisticated cases.

## Stack traces

As an essential part of debug information, stack traces are included in all *errorx* errors by default.

When an error is passed along, the original stack trace is simply retained, as this typically takes place along the lines of the same frames that were originally captured. When an error is received from another goroutine, use this to add frames that would otherwise be missing:

```go
return errorx.EnhanceStackTrace(<-errorChan, "task failed")
```

Result would look like this:
```
Error: task failed, cause: common.illegal_state: unfortunate
 at main.proxy()
	main.go:17
 at main.main()
	main.go:11
 ----------------------------------
 at main.culprit()
	main.go:26
 at main.innocent()
	main.go:21
  ```

On the other hand, some errors do not require a stack trace. Some may be used as a control flow mark, other are known to be benign. Stack trace could be omitted by not using the ```%+v``` formatting, but the better alternative is to modify the error type:

```go
ErrInvalidToken    = AuthErrors.NewType("invalid_token").ApplyModifiers(errorx.TypeModifierOmitStackTrace)
```

This way, a receiver of an error always treats it the same way, and it is the producer who modifies the behaviour. Following, again, the principle of opacity.

Other relevant tools include ```EnsureStackTrace(err)``` to provide an error of unknown nature with a stack trace, if it lacks one.

### Stack traces benchmark

As performance is obviously an issue, some measurements are in order. The benchmark is provided with the library. In all of benchmark cases, a very simple code is called that does nothing but grows a number of frames and immediately returns an error.

Result sample, MacBook Pro Intel Core i7-6920HQ CPU @ 2.90GHz 4 core:

name | runs | ns/op | note
------ | ------: | ------: | ------
BenchmarkSimpleError10                    | 20000000 |     57.2 | simple error, 10 frames deep
BenchmarkErrorxError10                    | 10000000 |      138 | same with errorx error
BenchmarkStackTraceErrorxError10          |  1000000 |     1601 | same with collected stack trace
BenchmarkSimpleError100                   |  3000000 |      421 | simple error, 100 frames deep
BenchmarkErrorxError100                   |  3000000 |      507 | same with errorx error
BenchmarkStackTraceErrorxError100         |   300000 |     4450 | same with collected stack trace
BenchmarkStackTraceNaiveError100-8         	   | 2000 |	    588135 | same with naive debug.Stack() error implementation
BenchmarkSimpleErrorPrint100              |  2000000 |      617 | simple error, 100 frames deep, format output
BenchmarkErrorxErrorPrint100              |  2000000 |      935 | same with errorx error
BenchmarkStackTraceErrorxErrorPrint100    |    30000 |    58965 | same with collected stack trace
BenchmarkStackTraceNaiveErrorPrint100-8    	   | 2000 |	    599155 | same with naive debug.Stack() error implementation

Key takeaways:
 * With deep enough call stack, trace capture brings **10x slowdown**
 * This is an absolute **worst case measurement, no-op function**; in a real life, much more time is spent doing actual work
 * Then again, in real life code invocation does not always result in error, so the overhead is proportional to the % of error returns
 * Still, it pays to omit stack trace collection when it would be of no use
 * It is actually **much more expensive to format** an error with a stack trace than to create it, roughly **another 10x**
 * Compared to the most naive approach to stack trace collection, error creation it is **100x** cheaper with errorx
 * Therefore, it is totally OK to create an error with a stack trace that would then be handled and not printed to log
 * Realistically, stack trace overhead is only painful either if a code is very hot (called a lot and returns errors often) or if an error is used as a control flow mechanism and does not constitute an actual problem; in both cases, stack trace should be omitted

## More

See [godoc](https://godoc.org/github.com/joomcode/errorx) for other *errorx* features:
* Namespaces
* Type switches
* ```errorx.Ignore```
* Trait inheritance
* Dynamic properties
* Panic-related utils
* Type registry
* etc.
