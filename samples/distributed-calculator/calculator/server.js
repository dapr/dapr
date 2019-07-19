const express = require('express');
const bodyParser = require('body-parser');
const fetch = require('isomorphic-fetch');
const path = require('path');

const app = express();
app.use(bodyParser.json());
app.use(bodyParser.urlencoded({
  extended: true
}));

const port = 8080;
const actionsUrl = `http://localhost:3500/action`;

app.post('/calculate/add', async (req, res) => {
  const addUrl = `${actionsUrl}/addapp/add`;
  await callAPI(addUrl, req.body, res);
});

app.post('/calculate/subtract', async (req, res) => {
  const subtractUrl = `${actionsUrl}/subtractapp/subtract`;
  await callAPI(subtractUrl, req.body, res);
});

app.post('/calculate/multiply', async (req, res) => {
  const multiplyUrl = `${actionsUrl}/multiplyapp/multiply`;
  await callAPI(multiplyUrl, req.body, res);
});

app.post('/calculate/divide', async (req, res) => {
  const divideUrl = `${actionsUrl}/divideapp/divide`;
  await callAPI(divideUrl, req.body, res);
});

const callAPI = async (url, body, res) => {
  const rawResponse = await fetch(url, {
    method: 'POST',
    headers: {
      'Content-Type': 'application/json',
    },
    body: JSON.stringify(body)
  });

  const response = await rawResponse.json();
  res.send(response.toString());
}

// Serve any static files
app.use(express.static(path.join(__dirname, 'client/build')));

// Handle React routing, return all requests to React app
app.get('*', function (req, res) {
  res.sendFile(path.join(__dirname, 'client/build', 'index.html'));
});

app.listen(process.env.PORT || port, () => console.log(`Listening on port ${port}!`));