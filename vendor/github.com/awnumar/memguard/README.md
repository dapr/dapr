<p align="center">
  <img src="https://cdn.rawgit.com/awnumar/memguard/master/logo.svg" height="140" />
  <h3 align="center">MemGuard</h3>
  <p align="center">Secure software enclave for storage of sensitive information in memory.</p>
  <p align="center">
    <a href="https://cirrus-ci.com/github/awnumar/memguard"><img src="https://api.cirrus-ci.com/github/awnumar/memguard.svg"></a>
    <a href="https://www.codacy.com/app/awnumar/memguard?utm_source=github.com&amp;utm_medium=referral&amp;utm_content=awnumar/memguard&amp;utm_campaign=Badge_Grade"><img src="https://api.codacy.com/project/badge/Grade/eebb7ecd6e794890999cfcf26328e9cb"/></a>
    <a href="https://godoc.org/github.com/awnumar/memguard"><img src="https://godoc.org/github.com/awnumar/memguard?status.svg"></a>
  </p>
</p>

---

This package attempts to reduce the likelihood of sensitive data being exposed. It supports all major operating systems and is written in pure Go.

## Features

* Sensitive data is encrypted and authenticated in memory using xSalsa20 and Poly1305 respectively. The scheme also defends against cold-boot attacks.
* Memory allocation bypasses the language runtime by using system calls to query the kernel for resources directly. This avoids interference from the garbage-collector.
* Buffers that store plaintext data are fortified with guard pages and canary values to detect spurious accesses and overflows.
* Effort is taken to prevent sensitive data from touching the disk. This includes locking memory to prevent swapping and handling core dumps.
* Kernel-level immutability is implemented so that attempted modification of protected regions results in an access violation.
* Multiple endpoints provide session purging and safe termination capabilities as well as signal handling to prevent remnant data being left behind.
* Side-channel attacks are mitigated against by making sure that the copying and comparison of data is done in constant-time.
* Accidental memory leaks are mitigated against by harnessing the garbage-collector to automatically destroy containers that have become unreachable.

Some features were inspired by [libsodium](https://github.com/jedisct1/libsodium), so credits to them.

Full documentation and a complete overview of the API can be found [here](https://godoc.org/github.com/awnumar/memguard). Interesting and useful code samples can be found within the [examples](examples) subpackage.

## Installation

```
$ go get github.com/awnumar/memguard
```

API is experimental and may have unstable changes. You should pin a version. [[modules](https://github.com/golang/go/wiki/Modules)]

## Contributing

* Submitting program samples to [`./examples`](examples).
* Reporting bugs, vulnerabilities, and any difficulties in using the API.
* Writing useful security and crypto libraries that utilise memguard.
* Implementing kernel-specific/cpu-specific protections.
* Submitting performance improvements.

Issues are for reporting bugs and for discussion on proposals. Pull requests should be made against master.
