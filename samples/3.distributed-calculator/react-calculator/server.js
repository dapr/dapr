const express = require('express');
const path = require('path');
const request = require('request');

const app = express();

const port = 8080;
const actionsUrl = "http://localhost:3500/v1.0/invoke";
const stateUrl = "http://localhost:3500/v1.0/state";

/**
The following routes forward requests (using pipe) from our React client to our actions-enabled services. Our actions sidecar lives on localhost:3500. We invoke other actions enabled services by calling /v1.0/invoke/<ACTIONS_ID>/method/<SERVICE'S_ROUTE>.
*/

app.post('/calculate/add', async (req, res) => {
  const addUrl = `${actionsUrl}/addapp/method/add`;
  req.pipe(request(addUrl)).pipe(res);
});

app.post('/calculate/subtract', async (req, res) => {
  const subtractUrl = `${actionsUrl}/subtractapp/method/subtract`;
  req.pipe(request(subtractUrl)).pipe(res);
});

app.post('/calculate/multiply', async (req, res) => {
  const multiplyUrl = `${actionsUrl}/multiplyapp/method/multiply`;
  req.pipe(request(multiplyUrl)).pipe(res);
});

app.post('/calculate/divide', async (req, res) => {
  const divideUrl = `${actionsUrl}/divideapp/method/divide`;
  req.pipe(request(divideUrl)).pipe(res);
});

// Forward state retrieval to Actions state endpoint
app.get('/state', async (req, res) => req.pipe(request(`${stateUrl}/calculatorState`)).pipe(res));

// Forward state persistence to Actions state endpoint
app.post('/persist', async (req, res) => req.pipe(request(stateUrl)).pipe(res));

// Serve static files
app.use(express.static(path.join(__dirname, 'client/build')));

// For all other requests, route to React client
app.get('*', function (_req, res) {
  res.sendFile(path.join(__dirname, 'client/build', 'index.html'));
});

app.listen(process.env.PORT || port, () => console.log(`Listening on port ${port}!`));
