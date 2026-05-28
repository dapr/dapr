@ECHO OFF
@REM Copyright 2022 The Dapr Authors
@REM Licensed under the Apache License, Version 2.0 (the "License");
@REM you may not use this file except in compliance with the License.
@REM You may obtain a copy of the License at
@REM     http://www.apache.org/licenses/LICENSE-2.0
@REM Unless required by applicable law or agreed to in writing, software
@REM distributed under the License is distributed on an "AS IS" BASIS,
@REM WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
@REM See the License for the specific language governing permissions and
@REM limitations under the License.

SET CERT_DIR=%SSL_CERT_DIR%
IF "%CERT_DIR%" == "" (
    ECHO SSL_CERT_DIR environment variable not set, skipping certificate setup
    EXIT /B 0
)

IF NOT EXIST "%CERT_DIR%" (
    ECHO SSL_CERT_DIR environment variable is not set to a valid path
    ECHO Found SSL_CERT_DIR="%CERT_DIR%"
    EXIT /B 1
)

SET FOUND_CERT=0

CD %CERT_DIR%
@REM Only attempt to install .pem and .crt files. certoc.exe will silently
@REM no-op on private keys (e.g. key.pem from a TLS bundle) but it is still
@REM noisy to invoke and can mask a real failure, so we narrow the glob.
FOR /R %%F IN (*.pem *.crt) DO (
    @REM A key.pem dropped in the same dir as cert.pem is a TLS bundle
    @REM private key; certoc -addstore root would reject it.
    IF /I NOT "%%~nxF" == "key.pem" (
        SET FOUND_CERT=1
        ECHO Adding %%F to the root store
        certoc.exe -addstore root %%F
    )
)
CD -

IF %FOUND_CERT% == 0 (
    ECHO No certificates found in %CERT_DIR%, skipping certificate setup
    EXIT /B 0
)