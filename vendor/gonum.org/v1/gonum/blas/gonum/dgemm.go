// Copyright ©2014 The Gonum Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package gonum

import (
	"runtime"
	"sync"

	"gonum.org/v1/gonum/blas"
	"gonum.org/v1/gonum/internal/asm/f64"
)

// Dgemm performs one of the matrix-matrix operations
//  C = alpha * A * B + beta * C
//  C = alpha * Aᵀ * B + beta * C
//  C = alpha * A * Bᵀ + beta * C
//  C = alpha * Aᵀ * Bᵀ + beta * C
// where A is an m×k or k×m dense matrix, B is an n×k or k×n dense matrix, C is
// an m×n matrix, and alpha and beta are scalars. tA and tB specify whether A or
// B are transposed.
func (Implementation) Dgemm(tA, tB blas.Transpose, m, n, k int, alpha float64, a []float64, lda int, b []float64, ldb int, beta float64, c []float64, ldc int) {
	switch tA {
	default:
		panic(badTranspose)
	case blas.NoTrans, blas.Trans, blas.ConjTrans:
	}
	switch tB {
	default:
		panic(badTranspose)
	case blas.NoTrans, blas.Trans, blas.ConjTrans:
	}
	if m < 0 {
		panic(mLT0)
	}
	if n < 0 {
		panic(nLT0)
	}
	if k < 0 {
		panic(kLT0)
	}
	aTrans := tA == blas.Trans || tA == blas.ConjTrans
	if aTrans {
		if lda < max(1, m) {
			panic(badLdA)
		}
	} else {
		if lda < max(1, k) {
			panic(badLdA)
		}
	}
	bTrans := tB == blas.Trans || tB == blas.ConjTrans
	if bTrans {
		if ldb < max(1, k) {
			panic(badLdB)
		}
	} else {
		if ldb < max(1, n) {
			panic(badLdB)
		}
	}
	if ldc < max(1, n) {
		panic(badLdC)
	}

	// Quick return if possible.
	if m == 0 || n == 0 {
		return
	}

	// For zero matrix size the following slice length checks are trivially satisfied.
	if aTrans {
		if len(a) < (k-1)*lda+m {
			panic(shortA)
		}
	} else {
		if len(a) < (m-1)*lda+k {
			panic(shortA)
		}
	}
	if bTrans {
		if len(b) < (n-1)*ldb+k {
			panic(shortB)
		}
	} else {
		if len(b) < (k-1)*ldb+n {
			panic(shortB)
		}
	}
	if len(c) < (m-1)*ldc+n {
		panic(shortC)
	}

	// Quick return if possible.
	if (alpha == 0 || k == 0) && beta == 1 {
		return
	}

	// scale c
	if beta != 1 {
		if beta == 0 {
			for i := 0; i < m; i++ {
				ctmp := c[i*ldc : i*ldc+n]
				for j := range ctmp {
					ctmp[j] = 0
				}
			}
		} else {
			for i := 0; i < m; i++ {
				ctmp := c[i*ldc : i*ldc+n]
				for j := range ctmp {
					ctmp[j] *= beta
				}
			}
		}
	}

	dgemmParallel(aTrans, bTrans, m, n, k, a, lda, b, ldb, c, ldc, alpha)
}

func dgemmParallel(aTrans, bTrans bool, m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	// dgemmParallel computes a parallel matrix multiplication by partitioning
	// a and b into sub-blocks, and updating c with the multiplication of the sub-block
	// In all cases,
	// A = [ 	A_11	A_12 ... 	A_1j
	//			A_21	A_22 ...	A_2j
	//				...
	//			A_i1	A_i2 ...	A_ij]
	//
	// and same for B. All of the submatrix sizes are blockSize×blockSize except
	// at the edges.
	//
	// In all cases, there is one dimension for each matrix along which
	// C must be updated sequentially.
	// Cij = \sum_k Aik Bki,	(A * B)
	// Cij = \sum_k Aki Bkj,	(Aᵀ * B)
	// Cij = \sum_k Aik Bjk,	(A * Bᵀ)
	// Cij = \sum_k Aki Bjk,	(Aᵀ * Bᵀ)
	//
	// This code computes one {i, j} block sequentially along the k dimension,
	// and computes all of the {i, j} blocks concurrently. This
	// partitioning allows Cij to be updated in-place without race-conditions.
	// Instead of launching a goroutine for each possible concurrent computation,
	// a number of worker goroutines are created and channels are used to pass
	// available and completed cases.
	//
	// http://alexkr.com/docs/matrixmult.pdf is a good reference on matrix-matrix
	// multiplies, though this code does not copy matrices to attempt to eliminate
	// cache misses.

	maxKLen := k
	parBlocks := blocks(m, blockSize) * blocks(n, blockSize)
	if parBlocks < minParBlock {
		// The matrix multiplication is small in the dimensions where it can be
		// computed concurrently. Just do it in serial.
		dgemmSerial(aTrans, bTrans, m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	}

	nWorkers := runtime.GOMAXPROCS(0)
	if parBlocks < nWorkers {
		nWorkers = parBlocks
	}
	// There is a tradeoff between the workers having to wait for work
	// and a large buffer making operations slow.
	buf := buffMul * nWorkers
	if buf > parBlocks {
		buf = parBlocks
	}

	sendChan := make(chan subMul, buf)

	// Launch workers. A worker receives an {i, j} submatrix of c, and computes
	// A_ik B_ki (or the transposed version) storing the result in c_ij. When the
	// channel is finally closed, it signals to the waitgroup that it has finished
	// computing.
	var wg sync.WaitGroup
	for i := 0; i < nWorkers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for sub := range sendChan {
				i := sub.i
				j := sub.j
				leni := blockSize
				if i+leni > m {
					leni = m - i
				}
				lenj := blockSize
				if j+lenj > n {
					lenj = n - j
				}

				cSub := sliceView64(c, ldc, i, j, leni, lenj)

				// Compute A_ik B_kj for all k
				for k := 0; k < maxKLen; k += blockSize {
					lenk := blockSize
					if k+lenk > maxKLen {
						lenk = maxKLen - k
					}
					var aSub, bSub []float64
					if aTrans {
						aSub = sliceView64(a, lda, k, i, lenk, leni)
					} else {
						aSub = sliceView64(a, lda, i, k, leni, lenk)
					}
					if bTrans {
						bSub = sliceView64(b, ldb, j, k, lenj, lenk)
					} else {
						bSub = sliceView64(b, ldb, k, j, lenk, lenj)
					}
					dgemmSerial(aTrans, bTrans, leni, lenj, lenk, aSub, lda, bSub, ldb, cSub, ldc, alpha)
				}
			}
		}()
	}

	// Send out all of the {i, j} subblocks for computation.
	for i := 0; i < m; i += blockSize {
		for j := 0; j < n; j += blockSize {
			sendChan <- subMul{
				i: i,
				j: j,
			}
		}
	}
	close(sendChan)
	wg.Wait()
}

// dgemmSerial is serial matrix multiply
func dgemmSerial(aTrans, bTrans bool, m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	switch {
	case !aTrans && !bTrans:
		dgemmSerialNotNot(m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	case aTrans && !bTrans:
		dgemmSerialTransNot(m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	case !aTrans && bTrans:
		dgemmSerialNotTrans(m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	case aTrans && bTrans:
		dgemmSerialTransTrans(m, n, k, a, lda, b, ldb, c, ldc, alpha)
		return
	default:
		panic("unreachable")
	}
}

// dgemmSerial where neither a nor b are transposed
func dgemmSerialNotNot(m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	// This style is used instead of the literal [i*stride +j]) is used because
	// approximately 5 times faster as of go 1.3.
	for i := 0; i < m; i++ {
		ctmp := c[i*ldc : i*ldc+n]
		for l, v := range a[i*lda : i*lda+k] {
			tmp := alpha * v
			if tmp != 0 {
				f64.AxpyUnitary(tmp, b[l*ldb:l*ldb+n], ctmp)
			}
		}
	}
}

// dgemmSerial where neither a is transposed and b is not
func dgemmSerialTransNot(m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	// This style is used instead of the literal [i*stride +j]) is used because
	// approximately 5 times faster as of go 1.3.
	for l := 0; l < k; l++ {
		btmp := b[l*ldb : l*ldb+n]
		for i, v := range a[l*lda : l*lda+m] {
			tmp := alpha * v
			if tmp != 0 {
				ctmp := c[i*ldc : i*ldc+n]
				f64.AxpyUnitary(tmp, btmp, ctmp)
			}
		}
	}
}

// dgemmSerial where neither a is not transposed and b is
func dgemmSerialNotTrans(m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	// This style is used instead of the literal [i*stride +j]) is used because
	// approximately 5 times faster as of go 1.3.
	for i := 0; i < m; i++ {
		atmp := a[i*lda : i*lda+k]
		ctmp := c[i*ldc : i*ldc+n]
		for j := 0; j < n; j++ {
			ctmp[j] += alpha * f64.DotUnitary(atmp, b[j*ldb:j*ldb+k])
		}
	}
}

// dgemmSerial where both are transposed
func dgemmSerialTransTrans(m, n, k int, a []float64, lda int, b []float64, ldb int, c []float64, ldc int, alpha float64) {
	// This style is used instead of the literal [i*stride +j]) is used because
	// approximately 5 times faster as of go 1.3.
	for l := 0; l < k; l++ {
		for i, v := range a[l*lda : l*lda+m] {
			tmp := alpha * v
			if tmp != 0 {
				ctmp := c[i*ldc : i*ldc+n]
				f64.AxpyInc(tmp, b[l:], ctmp, uintptr(n), uintptr(ldb), 1, 0, 0)
			}
		}
	}
}

func sliceView64(a []float64, lda, i, j, r, c int) []float64 {
	return a[i*lda+j : (i+r-1)*lda+j+c]
}
